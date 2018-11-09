/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package proxy

import (
	"context"

	"github.com/alipay/sofa-mosn/pkg/types"
)

func init() {
	RegisterMakeHandlerChain(DefaultMakeHandlerChain)
}

type RouteHandlerChain struct {
	ctx      context.Context
	handlers []types.RouteHandler
	index    int
}

func NewRouteHandlerChain(ctx context.Context, handlers []types.RouteHandler) *RouteHandlerChain {
	return &RouteHandlerChain{
		ctx:      ctx,
		handlers: handlers,
		index:    0,
	}
}

func (hc *RouteHandlerChain) DoNextHandler() types.Route {
	handler := hc.Next()
	if handler == nil {
		return nil
	}
	if handler.IsAvailable(hc.ctx) {
		return handler.Route()
	}
	return hc.DoNextHandler()
}
func (hc *RouteHandlerChain) Next() types.RouteHandler {
	if hc.index >= len(hc.handlers) {
		return nil
	}
	h := hc.handlers[hc.index]
	hc.index++
	return h
}

type MakeHandlerChain func(types.HeaderMap, types.Routers) *RouteHandlerChain

var makeHandlerChain MakeHandlerChain

func RegisterMakeHandlerChain(f MakeHandlerChain) {
	makeHandlerChain = f
}

type simpleHandler struct {
	route types.Route
}

func (h *simpleHandler) IsAvailable(ctx context.Context) bool {
	return true
}
func (h *simpleHandler) Route() types.Route {
	return h.route
}
func DefaultMakeHandlerChain(headers types.HeaderMap, routers types.Routers) *RouteHandlerChain {
	if r := routers.Route(headers, 1); r != nil {
		return NewRouteHandlerChain(context.Background(), []types.RouteHandler{
			&simpleHandler{route: r},
		})
	}
	return nil
}
