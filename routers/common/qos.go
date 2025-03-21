// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package common

import (
	"net/http"
	"slices"
	"strings"

	user_model "code.gitea.io/gitea/models/user"
	"code.gitea.io/gitea/modules/reqctx"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/modules/web/middleware"

	"github.com/bohde/codel"
	"github.com/go-chi/chi/v5"
)

// QoS implements quality of service for requests, based upon
// whether the user is logged in. All traffic may get dropped,
// and anonymous users are deprioritized.
func QoS() func(next http.Handler) http.Handler {
	if !setting.Service.QoS.Enabled {
		return nil
	}

	maxOutstanding := setting.Service.QoS.MaxInFlightRequests
	if maxOutstanding <= 0 {
		maxOutstanding = 10
	}

	c := codel.NewPriority(codel.Options{
		// The maximum number of waiting requests.
		MaxPending: setting.Service.QoS.MaxWaitingRequests,
		// The maximum number of in-flight requests.
		MaxOutstanding: maxOutstanding,
		// The target latency that a blocked request should wait
		// for. After this, it might be dropped.
		TargetLatency: setting.Service.QoS.TargetWaitTime,
	})

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			priority, longPolling := determineRequestPriority(reqctx.FromContext(req.Context()))

			// Check if the request can begin processing.
			err := c.Acquire(req.Context(), priority)
			if err != nil {
				// If it failed, the service is over capacity and should error
				http.Error(w, "Service Unavailable (QoS)", http.StatusServiceUnavailable)
				return
			}

			if longPolling {
				// Release long-polling immediately, so they don't always take up an in-flight request
				c.Release()
			} else {
				defer c.Release()
			}

			next.ServeHTTP(w, req)
		})
	}
}

func isRoutePathLowPriority(routePattern string) bool {
	subPath, ok := strings.CutPrefix(routePattern, "/{username}/{reponame}/")
	if !ok {
		return false
	}
	subPath, _, _ = strings.Cut(subPath, "/")
	parts := []string{
		"activity",
		"archive",
		"blame",
		"branches",
		"commit",
		"commits",
		"compare",
		"graph",
		"labels",
		"media",
		"raw",
		"search",
		"src",
		"stars",
		"tags",
		"watchers",
		"wiki",
	}
	return slices.Contains(parts, subPath)
}

func isRoutePathForLongPolling(routePattern string) bool {
	return routePattern == "/user/events"
}

// TODO: add some tests

func determineRequestPriority(reqCtx reqctx.RequestContext) (priority int, longPolling bool) {
	const priorityLow = -10
	const priorityHigh = 10

	chiRoutePath := chi.RouteContext(reqCtx).RoutePattern()
	if _, ok := reqCtx.GetData()[middleware.ContextDataKeySignedUser].(*user_model.User); ok {
		// If the user is logged in, assign high priority.
		priority = priorityHigh
	} else if isRoutePathLowPriority(chiRoutePath) {
		// Otherwise, if the path would is accessing git contents directly, mark as low priority
		priority = priorityLow
	}

	return priority, isRoutePathForLongPolling(chiRoutePath)
}
