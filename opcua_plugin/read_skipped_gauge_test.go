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
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/gopcua/opcua/ua"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A skipped node has to be visible OUTSIDE the process, not only in its log:
// the gauge opcua_node_skipped{node_id, reason} is 1 while the input skips the
// node and 0 once it closes or discovers again. It goes through the benthos
// metrics of the input (mgr.Metrics()), so Connect serves it on /metrics; the
// promauto counter opcua_subscription_failures_total lives in the default
// Prometheus registry, which Connect does not serve. Discovery skips (the
// common case: a configured NodeID the server does not have) count too.
type fakeGauge struct{ set map[string]int64 }

func (f *fakeGauge) Set(v int64, labels ...string) { f.set[strings.Join(labels, "|")] = v }

var _ = Describe("A skipped node is visible on /metrics", Label("node-scoped"), func() {
	BeforeEach(func() {
		if os.Getenv("TEST_OPCUA_UNIT") == "" {
			Skip("Skipping OPC UA unit tests: TEST_OPCUA_UNIT not set")
		}
	})

	It("a browse error of one node keeps its text, its status and names the node", func() {
		e1 := errOfNode("browse failed for", "ns=2;s=Typo", ua.StatusBadNodeIDUnknown)
		e2 := errOfNode("node", "ns=2;s=Locked", ua.StatusBadUserAccessDenied)
		Expect(e1.Error()).To(Equal("browse failed for ns=2;s=Typo: " + ua.StatusBadNodeIDUnknown.Error()))
		Expect(e2.Error()).To(Equal("node ns=2;s=Locked: " + ua.StatusBadUserAccessDenied.Error()))
		Expect(errors.Is(e1, ua.StatusBadNodeIDUnknown)).To(BeTrue())
		Expect(isNodeScoped(e2)).To(BeTrue())
	})

	It("skippedNodesOf names the node-scoped errors with a known node, the others not", func() {
		got := skippedNodesOf([]error{
			errOfNode("browse failed for", "ns=2;s=Typo", ua.StatusBadNodeIDUnknown),
			fmt.Errorf("wrapped: %w", errOfNode("node", "ns=2;s=Bad;", ua.StatusBadNodeIDInvalid)),
			errOfNode("browse failed for", "ns=2;s=Net", ua.StatusBadSecureChannelClosed),
			ua.StatusBadNodeIDUnknown,
			nil,
		})
		Expect(got).To(Equal([]skippedNode{{NodeID: "ns=2;s=Typo", Status: ua.StatusBadNodeIDUnknown}, {NodeID: "ns=2;s=Bad;", Status: ua.StatusBadNodeIDInvalid}}))
	})

	It("the tracker sets the gauge to 1 per skipped node with its reason, and back to 0 on clear", func() {
		g := &fakeGauge{set: map[string]int64{}}
		t := newSkipTracker(g)
		t.mark("ns=2;s=Typo", ua.StatusBadNodeIDUnknown)
		t.mark("ns=2;s=Locked", ua.StatusBadUserAccessDenied)
		t.mark("ns=2;s=Typo", ua.StatusBadNodeIDUnknown)
		Expect(g.set).To(Equal(map[string]int64{"ns=2;s=Typo|node_id_unknown": 1, "ns=2;s=Locked|access_denied": 1}))
		t.clear()
		Expect(g.set).To(Equal(map[string]int64{"ns=2;s=Typo|node_id_unknown": 0, "ns=2;s=Locked|access_denied": 0}))
		t.mark("ns=2;s=Typo", ua.StatusBadNodeIDUnknown)
		keys := []string{}
		for k, v := range g.set {
			if v == 1 {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		Expect(keys).To(Equal([]string{"ns=2;s=Typo|node_id_unknown"}))
	})

	It("a nil tracker is safe (an input built without metrics)", func() {
		var t *skipTracker
		Expect(func() { t.mark("x", ua.StatusBadNodeIDUnknown); t.clear() }).NotTo(Panic())
	})

	It("Close of the input clears what it skipped", func() {
		g := &fakeGauge{set: map[string]int64{}}
		in := &OPCUAInput{OPCUAConnection: &OPCUAConnection{}, skips: newSkipTracker(g)}
		in.skips.mark("ns=2;s=Typo", ua.StatusBadNodeIDUnknown)
		_ = in.clearSkipped(context.Background())
		Expect(g.set).To(Equal(map[string]int64{"ns=2;s=Typo|node_id_unknown": 0}))
	})
})

// B8 (0.6.3 live check on .99): a configured node the server does not have is refused
// by its NodeClass attribute status first; that error comes back through the
// browse worker unchanged and must still name its node, or the gauge stays empty.
var _ = Describe("The attribute status of an unknown node names the node", Label("node-scoped"), func() {
	BeforeEach(func() {
		if os.Getenv("TEST_OPCUA_UNIT") == "" {
			Skip("Skipping OPC UA unit tests: TEST_OPCUA_UNIT not set")
		}
	})
	It("skippedNodesOf finds the node of a BadNodeIdUnknown NodeClass status", func() {
		id := ua.MustParseNodeID("ns=1;s=FindingG-DoesNotExist")
		def := &NodeDef{NodeID: id}
		attrs := []*ua.DataValue{{Status: ua.StatusBadNodeIDUnknown}, {Status: ua.StatusBadNodeIDUnknown}, {Status: ua.StatusBadNodeIDUnknown}, {Status: ua.StatusBadNodeIDUnknown}, {Status: ua.StatusBadNodeIDUnknown}}
		err := processNodeAttributes(attrs, def, "root", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("node ns=1;s=FindingG-DoesNotExist attribute NodeClass"))
		Expect(skippedNodesOf([]error{err})).To(Equal([]skippedNode{{NodeID: "ns=1;s=FindingG-DoesNotExist", Status: ua.StatusBadNodeIDUnknown}}))
	})
})
