package main

// Stateful-clients plumbing: one JSON-RPC connection per client container over
// /clients/<name>.sock. A rule never blocks on a client for longer than its
// wait; outcomes that arrive later are delivered to the operation's callback,
// however late. A closed connection means the client restarted.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
)

const MaxInFlight = 16

type Outcome struct {
	Outcome string          `json:"outcome"` // ok | failed | unknown | lost
	Value   json.RawMessage `json:"value,omitempty"`
	Error   string          `json:"error,omitempty"`
	ErrKind string          `json:"err_kind,omitempty"`
}

type Status int

const (
	Done Status = iota
	Pending
	Lost
	NotSent
)

type op struct {
	label    string
	method   string
	sentAt   time.Time
	waiter   chan Outcome
	complete func(o Outcome, at time.Time)
}

type conn struct {
	name        string
	gen         int64
	nc          net.Conn
	wq          chan []byte
	mu          sync.Mutex
	outstanding map[int64]*op
	closed      bool
}

type Clients struct {
	names   []string
	mu      sync.Mutex
	conns   map[string]*conn
	nextID  atomic.Int64
	nextGen atomic.Int64
	active  *atomic.Bool
}

func NewClients(names []string, active *atomic.Bool) *Clients {
	return &Clients{names: names, conns: map[string]*conn{}, active: active}
}

func (cs *Clients) get(name string) *conn {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if c := cs.conns[name]; c != nil && !c.closed {
		return c
	}
	nc, err := net.DialTimeout("unix", "/clients/"+name+".sock", time.Second)
	if err != nil {
		return nil
	}
	c := &conn{name: name, gen: cs.nextGen.Add(1), nc: nc, wq: make(chan []byte, 256), outstanding: map[int64]*op{}}
	cs.conns[name] = c
	go func() {
		for b := range c.wq {
			if _, err := nc.Write(b); err != nil {
				return
			}
		}
	}()
	go cs.reader(c)
	return c
}

func (cs *Clients) reader(c *conn) {
	sc := bufio.NewScanner(c.nc)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var msg struct {
			ID     *int64          `json:"id"`
			Result *Outcome        `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil || msg.ID == nil {
			continue
		}
		now := time.Now()
		c.mu.Lock()
		o := c.outstanding[*msg.ID]
		delete(c.outstanding, *msg.ID)
		c.mu.Unlock()
		if o == nil {
			continue
		}
		var out Outcome
		if msg.Error != nil && string(msg.Error) != "null" {
			assert.Unreachable("a client can't run a request", map[string]any{"client": c.name, "method": o.method, "error": string(msg.Error)})
			out = Outcome{Outcome: "unknown", Error: string(msg.Error), ErrKind: "protocol"}
		} else if msg.Result != nil {
			out = *msg.Result
		}
		if o.method != "health" && o.method != "result" && o.method != "query" && o.method != "describe" && o.method != "history" && o.method != "list" {
			log.Printf("rpc %s %s -> %s %s %.120s", c.name, o.label, out.Outcome, out.ErrKind, string(out.Value))
		}
		if o.complete != nil {
			o.complete(out, now)
		}
		select {
		case o.waiter <- out:
		default:
		}
	}
	// Connection closed: the client process exited (restart).
	cs.mu.Lock()
	c.closed = true
	if cs.conns[c.name] == c {
		delete(cs.conns, c.name)
	}
	cs.mu.Unlock()
	c.mu.Lock()
	ops := c.outstanding
	c.outstanding = map[int64]*op{}
	c.mu.Unlock()
	close(c.wq)
	if cs.active.Load() {
		assert.Reachable("a client restarts", map[string]any{"client": c.name, "outstanding": len(ops)})
	}
	log.Printf("client %s connection closed (gen %d), %d ops lost", c.name, c.gen, len(ops))
	now := time.Now()
	for _, o := range ops {
		lost := Outcome{Outcome: "unknown", ErrKind: "lost", Error: "client restarted"}
		if o.complete != nil {
			o.complete(lost, now)
		}
		select {
		case o.waiter <- lost:
		default:
		}
	}
}

// Call sends one operation to a client and waits up to wait for it. complete
// is called exactly once if the op was sent (with a "lost" unknown outcome
// if the client restarts first), however late.
func (cs *Clients) Call(name, method string, params any, wait time.Duration, complete func(Outcome, time.Time)) (Status, Outcome) {
	c := cs.get(name)
	if c == nil {
		return NotSent, Outcome{}
	}
	id := cs.nextID.Add(1)
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		panic(fmt.Sprintf("marshal params: %v", err))
	}
	o := &op{method: method, label: opLabel(method, params), sentAt: time.Now(), waiter: make(chan Outcome, 1), complete: complete}
	c.mu.Lock()
	if len(c.outstanding) >= MaxInFlight {
		c.mu.Unlock()
		return NotSent, Outcome{}
	}
	c.outstanding[id] = o
	c.mu.Unlock()
	sent := true
	func() {
		defer func() { recover() }() // writer closed by a concurrent restart: reader delivers "lost"
		select {
		case c.wq <- append(b, '\n'):
		default:
			// writer backed up (paused client): don't send; the caller sees NotSent
			c.mu.Lock()
			delete(c.outstanding, id)
			c.mu.Unlock()
			sent = false
		}
	}()
	if !sent {
		return NotSent, Outcome{}
	}
	select {
	case out := <-o.waiter:
		if out.ErrKind == "lost" {
			return Lost, out
		}
		return Done, out
	case <-time.After(wait):
		return Pending, Outcome{}
	}
}

// Outstanding returns per-client outstanding op counts and the oldest age.
func (cs *Clients) Outstanding() map[string]any {
	cs.mu.Lock()
	conns := make([]*conn, 0, len(cs.conns))
	for _, c := range cs.conns {
		conns = append(conns, c)
	}
	cs.mu.Unlock()
	out := map[string]any{}
	for _, c := range conns {
		c.mu.Lock()
		oldest := time.Duration(0)
		methods := map[string]int{}
		for _, o := range c.outstanding {
			if a := time.Since(o.sentAt); a > oldest {
				oldest = a
			}
			methods[o.method]++
		}
		out[c.name] = map[string]any{"n": len(c.outstanding), "oldest_s": oldest.Seconds(), "methods": methods}
		c.mu.Unlock()
	}
	return out
}

func opLabel(method string, params any) string {
	if m, ok := params.(map[string]any); ok {
		l := fmt.Sprintf("%s wid=%v", method, m["wid"])
		if sig, ok := m["sig"].(map[string]any); ok {
			l += fmt.Sprintf(" sig=%v", sig["id"])
		}
		if n, ok := m["signal"]; ok {
			l += fmt.Sprintf(" signal=%v", n)
		}
		if r, ok := m["reuse"]; ok {
			l += fmt.Sprintf(" reuse=%v", r)
		}
		return l
	}
	return method
}
