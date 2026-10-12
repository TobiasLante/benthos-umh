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
	"strings"

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
