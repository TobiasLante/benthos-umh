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

	"github.com/gopcua/opcua/ua"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// One configured NodeID that the server does not have (a typo, another firmware)
// must not take the whole input down: discovery and monitoring skip that node,
// name it, and subscribe to every other node. An error of the connection or the
// session (not of one node) still fails as before, so the input reconnects.
var _ = Describe("A node the server does not have is skipped, not fatal", Label("node-scoped"), func() {
	BeforeEach(func() {
		if os.Getenv("TEST_OPCUA_UNIT") == "" {
			Skip("Skipping OPC UA unit tests: TEST_OPCUA_UNIT not set")
		}
	})

	It("classifies the status codes of one node as node-scoped", func() {
		for _, s := range []ua.StatusCode{ua.StatusBadNodeIDUnknown, ua.StatusBadNodeIDInvalid, ua.StatusBadUserAccessDenied} {
			Expect(isNodeScoped(s)).To(BeTrue(), "%v", s)
			Expect(isNodeScoped(fmt.Errorf("node ns=2;s=X: %w", s))).To(BeTrue(), "wrapped %v", s)
			Expect(isNodeScoped(fmt.Errorf("browse failed for ns=2;s=X: %w", s))).To(BeTrue(), "browse-wrapped %v", s)
		}
		for _, e := range []error{ua.StatusBadSecureChannelClosed, ua.StatusBadSessionIDInvalid, ua.StatusBadTimeout,
			context.DeadlineExceeded, errors.New("browse pool completion failed")} {
			Expect(isNodeScoped(e)).To(BeFalse(), "%v", e)
		}
	})

	It("discovery keeps the found nodes when only node-scoped errors came back", func() {
		skipped, fatal := splitBrowseErrors([]error{
			fmt.Errorf("node ns=2;s=Typo: %w", ua.StatusBadNodeIDUnknown),
			ua.StatusBadNodeIDInvalid,
		})
		Expect(fatal).To(BeNil())
		Expect(skipped).To(HaveLen(2))
		Expect(skipped[0]).To(ContainSubstring("ns=2;s=Typo"))
	})

	It("discovery still fails on an error of the connection", func() {
		_, fatal := splitBrowseErrors([]error{
			fmt.Errorf("node ns=2;s=Typo: %w", ua.StatusBadNodeIDUnknown),
			ua.StatusBadSecureChannelClosed,
		})
		Expect(fatal).To(HaveOccurred())
		Expect(fatal.Error()).To(ContainSubstring("SecureChannelClosed"))
	})

	It("monitoring skips a node the server refuses and goes on with the batch", func() {
		Expect(monitorResultAction(ua.StatusOK, false)).To(Equal(monitorOK))
		Expect(monitorResultAction(ua.StatusBadNodeIDUnknown, false)).To(Equal(monitorSkipNode))
		Expect(monitorResultAction(ua.StatusBadNodeIDUnknown, true)).To(Equal(monitorSkipNode))
		Expect(monitorResultAction(ua.StatusBadFilterNotAllowed, true)).To(Equal(monitorRetryWithoutFilter))
		Expect(monitorResultAction(ua.StatusBadFilterNotAllowed, false)).To(Equal(monitorAbort))
		Expect(monitorResultAction(ua.StatusBadTooManyMonitoredItems, false)).To(Equal(monitorAbort))
	})
})
