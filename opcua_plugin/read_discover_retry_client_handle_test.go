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
	"fmt"
	"os"

	"github.com/gopcua/opcua/ua"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ReadBatchSubscribe resolves every notification with g.NodeList[ClientHandle].
// The trial-and-retry path continues with nodes[failedNodeIndex:]; its handles
// must keep pointing at the same NodeList entries, otherwise every value after
// the failed node is published under the tag of another node.
var _ = Describe("MonitorBatched retry keeps ClientHandles aligned with NodeList", Label("retry-client-handle"), func() {
	BeforeEach(func() {
		if os.Getenv("TEST_OPCUA_UNIT") == "" {
			Skip("Skipping OPC UA unit tests: TEST_OPCUA_UNIT not set")
		}
	})

	It("maps every handle of the retry call to the node it monitors", func() {
		totalNodes, maxBatchSize, failedNodeIndex := 327, 50, 63

		nodeList := make([]NodeDef, totalNodes)
		for i := range nodeList {
			nodeList[i] = NodeDef{
				NodeID: ua.NewNumericNodeID(1, uint32(i+1000)),
				Path:   fmt.Sprintf("TestNode_%d", i),
			}
		}

		// First call: handles are the plain NodeList index.
		for _, b := range CalculateBatches(totalNodes, maxBatchSize) {
			for pos := range nodeList[b.Start:b.End] {
				h := clientHandleFor(0, b.Start, pos)
				Expect(nodeList[h].NodeID).To(Equal(nodeList[b.Start+pos].NodeID))
			}
		}

		// Retry call, exactly as monitorBatchedFrom recurses.
		retry := nodeList[failedNodeIndex:]
		for _, b := range CalculateBatches(len(retry), maxBatchSize) {
			for pos, node := range retry[b.Start:b.End] {
				h := clientHandleFor(failedNodeIndex, b.Start, pos)
				Expect(int(h)).To(BeNumerically("<", len(nodeList)))
				Expect(nodeList[h].NodeID).To(Equal(node.NodeID),
					"handle %d of retry position %d resolves to %s, but monitors %s",
					h, b.Start+pos, nodeList[h].Path, node.Path)
			}
		}
	})
})
