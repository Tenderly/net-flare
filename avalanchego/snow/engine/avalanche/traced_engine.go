// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package avalanche

import (
	"context"

	"go.opentelemetry.io/otel/attribute"

	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/tenderly/net-flare/avalanchego/ids"
	"github.com/tenderly/net-flare/avalanchego/snow/consensus/avalanche"
	"github.com/tenderly/net-flare/avalanchego/snow/engine/common"
	"github.com/tenderly/net-flare/avalanchego/trace"
)

var _ Engine = (*tracedEngine)(nil)

type tracedEngine struct {
	common.Engine
	engine Engine
	tracer trace.Tracer
}

func TraceEngine(engine Engine, tracer trace.Tracer) Engine {
	return &tracedEngine{
		Engine: common.TraceEngine(engine, tracer),
		engine: engine,
		tracer: tracer,
	}
}

func (e *tracedEngine) GetVtx(ctx context.Context, vtxID ids.ID) (avalanche.Vertex, error) {
	ctx, span := e.tracer.Start(ctx, "tracedEngine.GetVtx", oteltrace.WithAttributes(
		attribute.Stringer("vtxID", vtxID),
	))
	defer span.End()

	return e.engine.GetVtx(ctx, vtxID)
}
