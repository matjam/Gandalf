package embed

import (
	"fmt"

	"github.com/knights-analytics/hugot/backends"
)

// poolLocalOutput copies sentence vectors or mean-pools attended token vectors
// into independently owned slices before native output tensors are destroyed.
func poolLocalOutput(data []float32, count, sequence, dims int, inputs []backends.TokenizedInput) ([][]float32, error) {
	rows := max(sequence, 1)
	if len(data) != count*rows*dims || len(inputs) != count {
		return nil, fmt.Errorf("embedding output size does not match batch shape")
	}
	result := make([][]float32, count)
	for i := range count {
		vector := make([]float32, dims)
		result[i] = vector
		if sequence == 0 {
			copy(vector, data[i*dims:(i+1)*dims])
			continue
		}
		attended := 0
		for token := range sequence {
			mask := inputs[i].AttentionMask
			if len(mask) > 0 && (token >= len(mask) || mask[token] == 0) {
				continue
			}
			attended++
			offset := (i*sequence + token) * dims
			for dimension := range dims {
				vector[dimension] += data[offset+dimension]
			}
		}
		if attended == 0 {
			return nil, fmt.Errorf("embedding input %d has no attended tokens", i)
		}
		// Preserve the existing model's pooling convention, including its
		// denominator, so previously stored vectors remain comparable.
		if len(inputs[i].AttentionMask) > 0 {
			attended = inputs[i].MaxAttentionIndex + 1
		}
		if attended <= 0 {
			return nil, fmt.Errorf("embedding input %d has invalid attention length", i)
		}
		for dimension := range dims {
			vector[dimension] /= float32(attended)
		}
	}
	return result, nil
}
