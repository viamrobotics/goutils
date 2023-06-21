package perf

import (
	"go.opencensus.io/plugin/ocgrpc"
	ocstats "go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
	"google.golang.org/grpc/stats"
)

// See: https://opencensus.io/guides/grpc/go/#1

func registerGrpcViews() error {
	OrgID, _ := tag.NewKey("org-id")
	tagsView := &view.View{
		Name:        "server/rpc_message_counts",
		Description: "number of messages received  in each rpc call",
		Measure: ocstats.Int64("rpc_message_counts", "Number of messages received in each RPC. Has value 1 for non-streaming RPCs.",
			ocstats.UnitDimensionless),
		Aggregation: view.Count(),
		TagKeys:     []tag.Key{OrgID},
	}
	views := append(ocgrpc.DefaultServerViews, tagsView)
	return view.Register(views...)
}

// NewGrpcStatsHandler creates a new stats handler writing to opencensus.
//
// Example:
//
//	grpcServer, err := rpc.NewServer(logger, rpc.WithStatsHandler(perf.NewGrpcStatsHandler()))
//
// See further documentation here: https://opencensus.io/guides/grpc/go/
func NewGrpcStatsHandler() stats.Handler {
	return &ocgrpc.ServerHandler{
		IsPublicEndpoint: true,
	}
}
