package classifier

import (
	"context"
	"fmt"

	pb "github.com/llm-d/fleet-llm-d/pkg/classifier/gen"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type RankedSignal struct {
	Label string
	Score float64
}

type ClassifyResult struct {
	ClassifierID     string
	ModelRevision    string
	TaxonomyRevision string
	Status           string
	TopLabel         string
	TopScore         float64
	Margin           float64
	Ranked           []RankedSignal
}

type ClassifierClient interface {
	Classify(ctx context.Context, text string, requestID string) (*ClassifyResult, error)
	Close() error
}

func NewClassifierClient(endpoint string) (ClassifierClient, error) {
	if endpoint == "" {
		return &disabledClassifierClient{}, nil
	}
	conn, err := grpc.NewClient(endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("grpc dial %s: %w", endpoint, err)
	}
	return &grpcClassifierClient{
		conn:   conn,
		client: pb.NewClassifyClient(conn),
	}, nil
}

type grpcClassifierClient struct {
	conn   *grpc.ClientConn
	client pb.ClassifyClient
}

func (c *grpcClassifierClient) Classify(ctx context.Context, text string, requestID string) (*ClassifyResult, error) {
	resp, err := c.client.Classify(ctx, &pb.ClassifyRequest{
		RequestId: requestID,
		Context:   text,
	})
	if err != nil {
		return nil, fmt.Errorf("classify: %w", err)
	}
	return mapResponse(resp), nil
}

func (c *grpcClassifierClient) Close() error {
	return c.conn.Close()
}

func mapResponse(resp *pb.ClassifyResponse) *ClassifyResult {
	result := &ClassifyResult{
		ClassifierID:     resp.ClassifierId,
		ModelRevision:    resp.ModelRevision,
		TaxonomyRevision: resp.TaxonomyRevision,
		Status:           resp.Status.String(),
	}
	for _, s := range resp.Ranked {
		result.Ranked = append(result.Ranked, RankedSignal{
			Label: s.Label,
			Score: float64(s.Score),
		})
	}
	if len(result.Ranked) > 0 {
		result.TopLabel = result.Ranked[0].Label
		result.TopScore = result.Ranked[0].Score
		if len(result.Ranked) > 1 {
			result.Margin = result.Ranked[0].Score - result.Ranked[1].Score
		} else {
			result.Margin = result.Ranked[0].Score
		}
	}
	return result
}

type disabledClassifierClient struct{}

func (c *disabledClassifierClient) Classify(ctx context.Context, text string, requestID string) (*ClassifyResult, error) {
	return nil, nil
}

func (c *disabledClassifierClient) Close() error { return nil }

type MockClassifierClient struct {
	Result *ClassifyResult
	Err    error
	Calls  int
}

func (c *MockClassifierClient) Classify(ctx context.Context, text string, requestID string) (*ClassifyResult, error) {
	c.Calls++
	return c.Result, c.Err
}

func (c *MockClassifierClient) Close() error { return nil }
