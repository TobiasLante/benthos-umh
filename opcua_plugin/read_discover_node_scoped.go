// Copyright 2025 UMH Systems GmbH
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package opcua_plugin

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/gopcua/opcua/ua"
)

// nodeScopedStatus are the status codes that speak about ONE node, not about the
// connection or the session: the server does not have the node, the id is
// malformed, or this user may not read it. Such a node is skipped (and named in
// the log); every other node of the input is still discovered and monitored.
var nodeScopedStatus = []ua.StatusCode{
	ua.StatusBadNodeIDUnknown,
	ua.StatusBadNodeIDInvalid,
	ua.StatusBadUserAccessDenied,
}

// isNodeScoped reports whether err is a status of one node (see nodeScopedStatus).
func isNodeScoped(err error) bool {
	if err == nil {
		return false
	}
	for _, s := range nodeScopedStatus {
		if errors.Is(err, s) {
			return true
		}
	}
	return false
}

// splitBrowseErrors separates the errors the browse workers sent: the
// node-scoped ones are returned as messages (the nodes to skip); any other error
// is fatal for the discovery, all of them joined into one error as before.
func splitBrowseErrors(errs []error) (skipped []string, fatal error) {
	var other strings.Builder
	for _, err := range errs {
		if err == nil {
			continue
		}
		if isNodeScoped(err) {
			skipped = append(skipped, err.Error())
			continue
		}
		other.WriteString(err.Error() + "; ")
	}
	if other.Len() > 0 {
		return skipped, errors.New(other.String())
	}
	return skipped, nil
}

type monitorAction int

const (
	monitorOK monitorAction = iota
	monitorSkipNode
	monitorRetryWithoutFilter
	monitorAbort
)

// monitorResultAction decides what one MonitoredItem result means for the batch:
// OK; a node-scoped status ⇒ skip that node and go on; the filter trial refused
// ⇒ retry without the DataChangeFilter; anything else ⇒ abort as before.
func monitorResultAction(status ua.StatusCode, shouldTrial bool) monitorAction {
	switch {
	case errors.Is(status, ua.StatusOK):
		return monitorOK
	case isNodeScoped(status):
		return monitorSkipNode
	case shouldTrial && errors.Is(status, ua.StatusBadFilterNotAllowed):
		return monitorRetryWithoutFilter
	default:
		return monitorAbort
	}
}

// nodeErr is the error of one node from the browse workers: it keeps the text
// the workers always sent ("<prefix> <node>: <err>"), unwraps to the status,
// and names the node, so a skipped node can be counted by its id.
type nodeErr struct {
	NodeID string
	msg    string
	err    error
}

func (e *nodeErr) Error() string { return e.msg }
func (e *nodeErr) Unwrap() error { return e.err }

// errOfNode builds the error of one node ("node X: …", "browse failed for X: …").
func errOfNode(prefix, nodeID string, err error) error {
	return &nodeErr{NodeID: nodeID, msg: fmt.Sprintf("%s %s: %v", prefix, nodeID, err), err: err}
}

// skippedNode is one node the input leaves out, with the status the server sent.
type skippedNode struct {
	NodeID string
	Status ua.StatusCode
}

// skippedNodesOf names the node-scoped errors whose node is known, in order.
func skippedNodesOf(errs []error) []skippedNode {
	var out []skippedNode
	for _, err := range errs {
		if err == nil || !isNodeScoped(err) {
			continue
		}
		var ne *nodeErr
		var st ua.StatusCode
		if errors.As(err, &ne) && errors.As(err, &st) {
			out = append(out, skippedNode{NodeID: ne.NodeID, Status: st})
		}
	}
	return out
}

// gaugeSetter is the part of *service.MetricGauge the tracker uses.
type gaugeSetter interface {
	Set(value int64, labelValues ...string)
}

// skipTracker keeps the gauge opcua_node_skipped{node_id, reason}: 1 for every
// node the input skips now, 0 again once it closes or discovers anew. The gauge
// is one of the input's own metrics, so Connect serves it on /metrics.
type skipTracker struct {
	mu    sync.Mutex
	gauge gaugeSetter
	on    map[[2]string]struct{}
}

func newSkipTracker(g gaugeSetter) *skipTracker {
	return &skipTracker{gauge: g, on: map[[2]string]struct{}{}}
}

// mark counts the failure (opcua_subscription_failures_total) and sets the gauge.
func (t *skipTracker) mark(nodeID string, status ua.StatusCode) {
	RecordSubscriptionFailure(status, nodeID)
	if t == nil || t.gauge == nil {
		return
	}
	k := [2]string{nodeID, classifyFailureReason(status)}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.on[k] = struct{}{}
	t.gauge.Set(1, k[0], k[1])
}

// clear sets every marked node back to 0.
func (t *skipTracker) clear() {
	if t == nil || t.gauge == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for k := range t.on {
		t.gauge.Set(0, k[0], k[1])
	}
	t.on = map[[2]string]struct{}{}
}
