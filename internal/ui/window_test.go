package ui

import (
	"errors"
	"testing"
)

func TestIsLocalRuntimeError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "connection refused", err: errors.New("dial tcp 127.0.0.1:11434: connectex: No connection could be made"), want: false},
		{name: "missing model", err: errors.New("로컬 LLM 주소와 모델을 설정하세요"), want: false},
		{name: "llama crash", err: errors.New("llama-server process has terminated: exit status 0xc0000409"), want: true},
		{name: "stack overrun", err: errors.New("The system detected an overrun of a stack-based buffer"), want: true},
		{name: "forcibly closed", err: errors.New("wsarecv: An existing connection was forcibly closed by the remote host"), want: true},
		{name: "out of memory", err: errors.New("failed to allocate memory: out of memory"), want: true},
		{name: "cuda", err: errors.New("CUDA error: an illegal memory access was encountered"), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLocalRuntimeError(tt.err); got != tt.want {
				t.Errorf("isLocalRuntimeError() = %v, want %v", got, tt.want)
			}
		})
	}
}
