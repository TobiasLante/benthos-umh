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
	"time"

	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/ua"
	"github.com/redpanda-data/benthos/v4/public/service"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// B20 (UNSaC 0.6.6 P18): after a long OPC UA server outage the subscription channel delivers StatusChangeNotification
// (BadShutdown / BadTimeout …) or an error ("next sequence number not in retransmission buffer") for ever, and the reader
// stays silent until its container restarts. ReadBatch must hand such a result to benthos as ErrNotConnected after closing
// the client, so Connect builds a new session and subscription. A good status and data changes are unchanged.
var _ = Describe("ReadBatch after a lost subscription", Label("b20-resubscribe"), func() {
	var (
		g       *OPCUAInput
		cleaned int
	)
	BeforeEach(func() {
		mgr := service.MockResources()
		cleaned = 0
		g = &OPCUAInput{
			OPCUAConnection:  &OPCUAConnection{Log: mgr.Logger()},
			NodeList:         []NodeDef{{NodeID: ua.NewNumericNodeID(1, 1001), Path: "TestNode_0"}},
			SubscribeEnabled: true,
			SubNotifyChan:    make(chan *opcua.PublishNotificationData, 1),
			skips:            newSkipTracker(mgr.Metrics().NewGauge("opcua_node_skipped", "node_id", "reason")),
		}
		g.cleanup_func = func(context.Context) { cleaned++ }
	})
	read := func(res *opcua.PublishNotificationData) (service.MessageBatch, error) {
		g.SubNotifyChan <- res
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		msgs, _, err := g.ReadBatch(ctx)
		return msgs, err
	}

	for _, status := range []ua.StatusCode{ua.StatusBadShutdown, ua.StatusBadTimeout, ua.StatusBadServerHalted, ua.StatusBadSessionClosed} {
		status := status
		It("a StatusChangeNotification "+status.Error()+" closes the client and asks benthos to reconnect", func() {
			_, err := read(&opcua.PublishNotificationData{Value: &ua.StatusChangeNotification{Status: status}})
			Expect(errors.Is(err, service.ErrNotConnected)).To(BeTrue(), "got %v", err)
			Expect(cleaned).To(Equal(1), "the old subscription is cleaned up once")
		})
	}

	// the .111 reproduction with 0.1.9 (40 min outage of the real machines-sgm): the client's own reconnect TRANSFERS the subscription
	// (StatusGoodSubscriptionTransferred, a "good" status) — then "next sequence number not in retransmission buffer" and no value ever
	It("a StatusChangeNotification GoodSubscriptionTransferred closes the client and asks benthos to reconnect", func() {
		_, err := read(&opcua.PublishNotificationData{Value: &ua.StatusChangeNotification{Status: ua.StatusGoodSubscriptionTransferred}})
		Expect(errors.Is(err, service.ErrNotConnected)).To(BeTrue(), "got %v", err)
		Expect(cleaned).To(Equal(1))
	})

	It("an error from the subscription channel closes the client and asks benthos to reconnect", func() {
		_, err := read(&opcua.PublishNotificationData{Error: errors.New("next sequence number not in retransmission buffer")})
		Expect(errors.Is(err, service.ErrNotConnected)).To(BeTrue(), "got %v", err)
		Expect(cleaned).To(Equal(1))
	})

	It("TWIN: a StatusChangeNotification with a good status keeps the connection", func() {
		msgs, err := read(&opcua.PublishNotificationData{Value: &ua.StatusChangeNotification{Status: ua.StatusOK}})
		Expect(err).NotTo(HaveOccurred())
		Expect(msgs).To(BeEmpty())
		Expect(cleaned).To(Equal(0))
	})

	It("TWIN: a data change still becomes a message, nothing is closed", func() {
		v := ua.MustVariant(int32(42))
		msgs, err := read(&opcua.PublishNotificationData{Value: &ua.DataChangeNotification{MonitoredItems: []*ua.MonitoredItemNotification{
			{ClientHandle: 0, Value: &ua.DataValue{Value: v, Status: ua.StatusOK, EncodingMask: ua.DataValueValue}}}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(msgs).To(HaveLen(1))
		Expect(cleaned).To(Equal(0))
	})
})
