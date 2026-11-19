package main

// Cross-cluster (XDC) reads: a run's history straight from this client's cluster's own store.
// The workflow API's GetWorkflowExecutionHistory is forwarded to the active cluster under the
// all-domain-apis-forwarding policy, so it can't show what a passive cluster holds. The admin API
// (GetWorkflowExecutionRawHistoryV2) isn't forwarded: it reads the local current branch.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	adminv1 "github.com/uber/cadence-idl/go/proto/admin/v1"
	apiv1 "github.com/uber/cadence-idl/go/proto/api/v1"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/thriftrw/protocol"
	"go.uber.org/thriftrw/protocol/binary"
	"go.uber.org/yarpc/yarpcerrors"
)

// RawEv is one event as stored: identity, version, and a digest of everything else except TaskId
// (a per-cluster task sequence number, not part of the replicated history).
type RawEv struct {
	ID      int64  `json:"id"`
	Type    string `json:"type"`
	Version int64  `json:"version"`
	Ts      int64  `json:"ts"`
	Digest  string `json:"digest"`
}

func rawHistory(ctx context.Context, admin adminv1.AdminAPIYARPCClient, p P) Result {
	if admin == nil {
		return Result{Outcome: "unknown", ErrKind: "no_client", Error: "admin client not ready"}
	}
	var evs []RawEv
	var token []byte
	for {
		resp, err := admin.GetWorkflowExecutionRawHistoryV2(ctx, &adminv1.GetWorkflowExecutionRawHistoryV2Request{
			Domain:            Domain,
			WorkflowExecution: &apiv1.WorkflowExecution{WorkflowId: p.WID, RunId: p.RunID},
			PageSize:          1000,
			NextPageToken:     token,
		})
		if err != nil {
			return classify(protoErr(err))
		}
		for _, b := range resp.HistoryBatches {
			if b.EncodingType != apiv1.EncodingType_ENCODING_TYPE_THRIFTRW || len(b.Data) < 1 {
				return Result{Outcome: "unknown", ErrKind: "decode", Error: fmt.Sprintf("encoding %v len %d", b.EncodingType, len(b.Data))}
			}
			var h s.History
			// Cadence's thriftrw blobs start with a one-byte version preamble.
			if err := h.Decode(binary.Default.Reader(bytes.NewReader(b.Data[1:]))); err != nil {
				return Result{Outcome: "unknown", ErrKind: "decode", Error: err.Error()}
			}
			for _, e := range h.Events {
				e.TaskId = nil
				var buf bytes.Buffer
				w, _ := e.ToWire()
				_ = protocol.Binary.Encode(w, &buf)
				sum := sha256.Sum256(buf.Bytes())
				evs = append(evs, RawEv{ID: e.GetEventId(), Type: e.GetEventType().String(), Version: e.GetVersion(),
					Ts: e.GetTimestamp() / 1e6, Digest: hex.EncodeToString(sum[:8])})
			}
		}
		if len(resp.NextPageToken) == 0 {
			break
		}
		token = resp.NextPageToken
	}
	return ok(evs)
}

// protoErr maps a raw proto API error to the thrift error types classify knows.
func protoErr(err error) error {
	if !yarpcerrors.IsStatus(err) {
		return err
	}
	st := yarpcerrors.FromError(err)
	switch st.Code() {
	case yarpcerrors.CodeNotFound:
		return &s.EntityNotExistsError{Message: st.Message()}
	case yarpcerrors.CodeInvalidArgument:
		return &s.BadRequestError{Message: st.Message()}
	case yarpcerrors.CodeResourceExhausted:
		return &s.ServiceBusyError{Message: st.Message()}
	case yarpcerrors.CodeInternal:
		return &s.InternalServiceError{Message: st.Message()}
	}
	return err
}
