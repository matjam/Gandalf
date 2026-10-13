//go:build !cgo || (!ORT && !ALL)

package embed

import (
	"context"
	"fmt"

	"github.com/knights-analytics/hugot/pipelines"
)

func runLocalPipeline(context.Context, *pipelines.FeatureExtractionPipeline, []string) ([][]float32, error) {
	return nil, fmt.Errorf("local embeddings require a native ONNX Runtime build")
}
