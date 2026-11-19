package main

// Cross-cluster replication (XDC): which cluster each client talks to, the failover rule, and the
// eventually's convergence checks (both clusters agree on the domain, and every closed run's
// history is the same in both clusters' stores).

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"hegel.dev/go/hegel"
)

const (
	FailoverCooldown = 60 * time.Second
	GracefulTimeoutS = 20
)

var Clusters = []string{"cluster0", "cluster1"}

type XDC struct {
	mu        sync.Mutex
	cluster   map[string]string   // client -> cluster
	byCluster map[string][]string // cluster -> clients
	lastTry   time.Time           // last failover attempt (cooldown)
	lastOK    time.Time           // last acknowledged failover
	active    string              // active cluster as last reported by Cadence
	inFlight  bool
	failovers int
}

type domainInfo struct {
	Name     string `json:"name"`
	IsGlobal bool   `json:"is_global"`
	Active   string `json:"active_cluster"`
	Version  int64  `json:"failover_version"`
	Cluster  string `json:"cluster"`
}

// newXDC parses CLIENT_CLUSTERS ("xclient1=cluster0,xclient3=cluster1"); unlisted clients use cluster0.
func newXDC(names []string, spec string) *XDC {
	x := &XDC{cluster: map[string]string{}, byCluster: map[string][]string{}, active: "cluster0"}
	m := map[string]string{}
	for _, kv := range strings.Split(spec, ",") {
		if k, v, ok := strings.Cut(strings.TrimSpace(kv), "="); ok {
			m[k] = v
		}
	}
	for _, n := range names {
		c := m[n]
		if c == "" {
			c = "cluster0"
		}
		x.cluster[n] = c
		x.byCluster[c] = append(x.byCluster[c], n)
	}
	log.Printf("xdc: clients by cluster %v", x.byCluster)
	return x
}

func (x *XDC) clientsOf(c string) []string { return x.byCluster[c] }

func (x *XDC) workloadStarted(t time.Time) {
	x.mu.Lock()
	x.lastTry = t.Add(-FailoverCooldown / 2) // the first failover can come 30 s into the workload
	x.mu.Unlock()
}

// noteClosed marks workflows that were open across an acknowledged failover.
func (x *XDC) noteClosed(w *WF) {
	x.mu.Lock()
	last := x.lastOK
	x.mu.Unlock()
	if !last.IsZero() && w.Created.Before(last) {
		assert.Sometimes(true, "a workflow open during a failover closes", map[string]any{"type": w.Type})
	}
}

// RuleFailover, rarely and at most once per cooldown: flip the domain's active cluster.
// The request goes to a client of the cluster becoming active (Cadence requires that for a
// graceful failover). Most failovers are graceful; some are forced.
func (d *Driver) RuleFailover(tc hegel.TestCase) {
	if d.busy() || !chance(tc, 0.02) {
		return
	}
	x := d.xdc
	x.mu.Lock()
	if x.inFlight || time.Since(x.lastTry) < FailoverCooldown {
		x.mu.Unlock()
		return
	}
	x.inFlight = true
	x.lastTry = time.Now()
	believed := x.active
	x.mu.Unlock()
	release := func() { x.mu.Lock(); x.inFlight = false; x.mu.Unlock() }

	target := "cluster1"
	if believed == "cluster1" {
		target = "cluster0"
	}
	clients := x.clientsOf(target)
	if len(clients) == 0 {
		release()
		return
	}
	at := clients[hegel.Draw(tc, hegel.Integers(0, len(clients)-1))]
	graceful := 0
	if chance(tc, 0.7) {
		graceful = GracefulTimeoutS
	}
	log.Printf("xdc: failover %s -> %s via %s (graceful_s=%d)", believed, target, at, graceful)
	st, _ := d.cs.Call(at, "failover", map[string]any{"cluster": target, "graceful_s": graceful}, RuleWait, func(o Outcome, _ time.Time) {
		defer release()
		log.Printf("xdc: failover to %s: %s %s %.200s", target, o.Outcome, o.ErrKind, o.Error)
		x.mu.Lock()
		if o.Outcome == "ok" {
			x.active = target
			x.lastOK = time.Now()
			x.failovers++
		}
		x.mu.Unlock()
		if o.Outcome == "ok" {
			// Constant messages: the SDK catalogs each assertion by its literal message.
			if target == "cluster1" {
				assert.Sometimes(true, "a global domain fails over to cluster1", nil)
			} else {
				assert.Sometimes(true, "a global domain fails over to cluster0", nil)
			}
			if graceful > 0 {
				assert.Sometimes(true, "a graceful failover is accepted", nil)
			} else {
				assert.Sometimes(true, "a force failover is accepted", nil)
			}
		}
		if o.Outcome == "failed" && strings.Contains(o.Error, "active cluster") {
			// Our belief was stale (e.g. an unknown outcome took effect): re-learn it.
			go d.refreshActive(at)
		}
	})
	if st == NotSent {
		release()
	} else if st == Lost {
		d.refreshActive(at)
	}
}

// refreshActive asks a client which cluster is active and records the answer.
func (d *Driver) refreshActive(client string) {
	d.cs.Call(client, "describe_domain", nil, RuleWait, func(o Outcome, _ time.Time) {
		var v domainInfo
		if o.Outcome == "ok" && json.Unmarshal(o.Value, &v) == nil && v.Active != "" {
			d.xdc.mu.Lock()
			d.xdc.active = v.Active
			d.xdc.mu.Unlock()
		}
	})
}

// RuleDescribeDomain reads the domain from a drawn client's own cluster; tracks the active
// cluster so failovers alternate, and marks when the two clusters disagree for a while.
func (d *Driver) RuleDescribeDomain(tc hegel.TestCase) {
	if d.busy() || !chance(tc, 0.1) {
		return
	}
	c := d.client(tc)
	d.cs.Call(c, "describe_domain", nil, RuleWait, func(o Outcome, _ time.Time) {
		var v domainInfo
		if o.Outcome != "ok" || json.Unmarshal(o.Value, &v) != nil {
			return
		}
		assert.Always(v.IsGlobal, "the domain stays global in every cluster", map[string]any{"client": c, "domain": v})
		d.xdc.mu.Lock()
		if d.xdc.cluster[c] == v.Active {
			d.xdc.active = v.Active // the active cluster knows it's active
		}
		d.xdc.mu.Unlock()
		assert.Sometimes(v.Cluster == "cluster1" && v.Active == "cluster1", "the passive cluster becomes active", nil)
	})
}

// ---------- eventually ----------

type rawEv struct {
	ID      int64  `json:"id"`
	Type    string `json:"type"`
	Version int64  `json:"version"`
	Ts      int64  `json:"ts"`
	Digest  string `json:"digest"`
}

func (d *Driver) eventuallyXDC(closedWFs []*WF) {
	x := d.xdc
	// a. Both clusters agree on the domain: same active cluster, same failover version.
	deadline := time.Now().Add(2 * time.Minute)
	var views map[string]domainInfo
	agree := false
	for {
		views = map[string]domainInfo{}
		for _, c := range Clusters {
			for _, cl := range x.clientsOf(c) {
				st, o := d.cs.Call(cl, "describe_domain", nil, 20*time.Second, nil)
				var v domainInfo
				if st == Done && o.Outcome == "ok" && json.Unmarshal(o.Value, &v) == nil {
					views[c] = v
					break
				}
			}
		}
		v0, ok0 := views["cluster0"]
		v1, ok1 := views["cluster1"]
		agree = ok0 && ok1 && v0.Active == v1.Active && v0.Version == v1.Version && v0.IsGlobal && v1.IsGlobal
		if agree || time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	x.mu.Lock()
	nfail := x.failovers
	x.mu.Unlock()
	assert.Always(agree, "both clusters agree on the domain's active cluster once faults stop",
		map[string]any{"views": views, "failovers": nfail})
	assert.Sometimes(nfail > 0, "a timeline with a failover reaches the replication check", nil)

	// b. Replication converges: each closed run's history in cluster1's store equals cluster0's.
	c0, c1 := x.clientsOf("cluster0"), x.clientsOf("cluster1")
	if len(c0) == 0 || len(c1) == 0 {
		return
	}
	seen := map[string]bool{}
	var runs [][2]string
	for _, w := range closedWFs {
		if w.ClosedRunID == "" || seen[w.WID+"/"+w.ClosedRunID] {
			continue
		}
		seen[w.WID+"/"+w.ClosedRunID] = true
		runs = append(runs, [2]string{w.WID, w.ClosedRunID})
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i][0] < runs[j][0] })
	deadline = time.Now().Add(3 * time.Minute)
	var diverged []map[string]any
	compared := 0
	for _, r := range runs {
		params := map[string]any{"wid": r[0], "run_id": r[1], "timeout_ms": 15000}
		var last map[string]any
		for i := 0; ; i++ {
			h0, e0 := d.rawHistory(c0[i%len(c0)], params)
			h1, e1 := d.rawHistory(c1[i%len(c1)], params)
			if e0 == "" && e1 == "" {
				if diff := diffHistories(h0, h1); diff == "" {
					compared++
					last = nil
					break
				} else {
					last = map[string]any{"wid": r[0], "run_id": r[1], "diff": diff, "n0": len(h0), "n1": len(h1)}
				}
			} else {
				last = map[string]any{"wid": r[0], "run_id": r[1], "cluster0_err": e0, "cluster1_err": e1}
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(2 * time.Second)
		}
		if last != nil {
			diverged = append(diverged, last)
		}
	}
	log.Printf("eventually: xdc compared %d runs, %d diverged", compared, len(diverged))
	if len(diverged) > 10 {
		diverged = diverged[:10]
	}
	assert.Always(len(diverged) == 0, "a closed workflow's history is the same in both clusters once faults stop",
		map[string]any{"diverged": diverged, "compared": compared, "runs": len(runs), "failovers": nfail})
	assert.Sometimes(compared > 0, "closed workflow histories are compared across clusters", nil)
}

func (d *Driver) rawHistory(client string, params map[string]any) ([]rawEv, string) {
	st, o := d.cs.Call(client, "raw_history", params, 30*time.Second, nil)
	if st != Done || o.Outcome != "ok" {
		return nil, fmt.Sprintf("%d %s %s %.200s", st, o.Outcome, o.ErrKind, o.Error)
	}
	var evs []rawEv
	if err := json.Unmarshal(o.Value, &evs); err != nil {
		return nil, "decode: " + err.Error()
	}
	return evs, ""
}

// diffHistories returns "" if the histories are equal, else the first difference.
func diffHistories(a, b []rawEv) string {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return fmt.Sprintf("event %d: cluster0 %+v cluster1 %+v", i+1, a[i], b[i])
		}
	}
	if len(a) != len(b) {
		return fmt.Sprintf("length cluster0 %d cluster1 %d", len(a), len(b))
	}
	return ""
}
