//go:build cgo && (ORT || ALL)

package embed

import (
	"context"
	"errors"
	"fmt"

	"github.com/knights-analytics/hugot/backends"
	"github.com/knights-analytics/hugot/pipelines"
	ort "github.com/yalue/onnxruntime_go"
)

// runLocalPipeline owns all native tensors and waits for inference to finish
// before releasing them. Hugot's generic runner returns on cancellation while
// native inference still uses its inputs, and does not destroy allocated outputs.
func runLocalPipeline(ctx context.Context, pipeline *pipelines.FeatureExtractionPipeline, texts []string) (embeddings [][]float32, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	batch := backends.NewBatch(len(texts))
	defer func() { err = errors.Join(err, batch.Destroy()) }()
	pipeline.Model.Tokenizer.MaxAllowedTokens = localWindow
	backends.TokenizeInputs(batch, pipeline.Model.Tokenizer, texts)
	if batch.MaxSequenceLength > localWindow {
		return nil, fmt.Errorf("tokenized input exceeds model window: %d > %d", batch.MaxSequenceLength, localWindow)
	}
	if err := createLocalInputs(batch, pipeline.Model); err != nil {
		return nil, err
	}
	outputs := make([]ort.Value, len(pipeline.Model.OutputsMeta))
	defer func() {
		for _, output := range outputs {
			if output != nil {
				err = errors.Join(err, output.Destroy())
			}
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := pipeline.Model.ORTModel.Session.Run(batch.InputValues.([]ort.Value), outputs); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if pipeline.OutputIndex < 0 || pipeline.OutputIndex >= len(outputs) {
		return nil, fmt.Errorf("invalid embedding output index %d", pipeline.OutputIndex)
	}
	output, ok := outputs[pipeline.OutputIndex].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("embedding output has type %T, expected float32 tensor", outputs[pipeline.OutputIndex])
	}
	shape := output.GetShape()
	if len(shape) < 2 || len(shape) > 3 || shape[0] != int64(len(texts)) || shape[len(shape)-1] != localDims {
		return nil, fmt.Errorf("unexpected embedding output shape %v", shape)
	}
	sequence := 0
	if len(shape) == 3 {
		if shape[1] <= 0 || shape[1] > localWindow {
			return nil, fmt.Errorf("invalid embedding sequence dimension %d", shape[1])
		}
		sequence = int(shape[1])
	}
	return poolLocalOutput(output.GetData(), len(texts), sequence, localDims, batch.Input)
}

// Register cleanup before allocating the first tensor so partial construction
// errors also release everything already allocated.
func createLocalInputs(batch *backends.PipelineBatch, model *backends.Model) error {
	inputs := make([]ort.Value, len(model.InputsMeta))
	batch.InputValues = inputs
	batch.DestroyInputs = func() error {
		var err error
		for _, input := range inputs {
			if input != nil {
				err = errors.Join(err, input.Destroy())
			}
		}
		return err
	}
	for index, meta := range model.InputsMeta {
		data := make([]int64, batch.Size*batch.MaxSequenceLength)
		for row, input := range batch.Input {
			for position := range batch.MaxSequenceLength {
				offset := row*batch.MaxSequenceLength + position
				switch meta.Name {
				case "input_ids":
					if position < len(input.TokenIDs) {
						data[offset] = int64(input.TokenIDs[position])
					}
				case "token_type_ids":
					if position < len(input.TypeIDs) {
						data[offset] = int64(input.TypeIDs[position])
					}
				case "attention_mask":
					if position < len(input.AttentionMask) {
						data[offset] = int64(input.AttentionMask[position])
					}
				case "position_ids":
					data[offset] = int64(position + 1)
				default:
					return fmt.Errorf("unsupported embedding input %q", meta.Name)
				}
			}
		}
		tensor, err := ort.NewTensor(ort.NewShape(int64(batch.Size), int64(batch.MaxSequenceLength)), data)
		if err != nil {
			return err
		}
		inputs[index] = tensor
	}
	return nil
}
