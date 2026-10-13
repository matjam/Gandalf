package embed

import (
	"reflect"
	"testing"

	"github.com/knights-analytics/hugot/backends"
)

func TestPoolLocalOutput(t *testing.T) {
	tests := []struct {
		name     string
		data     []float32
		sequence int
		inputs   []backends.TokenizedInput
		want     [][]float32
		wantErr  bool
	}{
		{name: "sentence vectors copied", data: []float32{1, 2, 3, 4}, inputs: make([]backends.TokenizedInput, 2), want: [][]float32{{1, 2}, {3, 4}}},
		{name: "padding excluded per input", data: []float32{2, 4, 4, 8, 100, 100, 6, 12, 100, 100, 100, 100}, sequence: 3, inputs: []backends.TokenizedInput{{AttentionMask: []uint32{1, 1, 0}, MaxAttentionIndex: 1}, {AttentionMask: []uint32{1, 0, 0}}}, want: [][]float32{{3, 6}, {6, 12}}},
		{name: "no mask averages all tokens", data: []float32{2, 4, 4, 8}, sequence: 1, inputs: make([]backends.TokenizedInput, 2), want: [][]float32{{2, 4}, {4, 8}}},
		{name: "invalid output rejected", data: []float32{1}, inputs: make([]backends.TokenizedInput, 2), wantErr: true},
		{name: "empty mask rejected", data: []float32{1, 2, 3, 4}, sequence: 1, inputs: []backends.TokenizedInput{{AttentionMask: []uint32{0}}, {AttentionMask: []uint32{0}}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := poolLocalOutput(tt.data, 2, tt.sequence, 2, tt.inputs)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, want error %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			tt.data[0] = -1000
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatal("output aliases native tensor data")
			}
		})
	}
}
