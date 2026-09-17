package mutation

import (
	"bytes"
	"context"

	"gopkg.in/yaml.v3"
)

// guardedYAMLStreamDecoder is the sole mutation-owned yaml.Decoder boundary.
// A decoded value is never returned until the public bundle graph guard has
// accepted it with the request context.
type guardedYAMLStreamDecoder struct {
	ctx     context.Context
	decoder *yaml.Decoder
}

func newGuardedYAMLStreamDecoder(ctx context.Context, source []byte) (*guardedYAMLStreamDecoder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &guardedYAMLStreamDecoder{ctx: ctx, decoder: yaml.NewDecoder(bytes.NewReader(source))}, nil
}

func (decoder *guardedYAMLStreamDecoder) decode() (*yaml.Node, error) {
	if decoder == nil || decoder.ctx == nil || decoder.decoder == nil {
		return nil, context.Canceled
	}
	if err := decoder.ctx.Err(); err != nil {
		return nil, err
	}
	var node yaml.Node
	if err := decoder.decoder.Decode(&node); err != nil {
		return nil, err
	}
	if err := decoder.ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateMutationYAMLGraphContext(decoder.ctx, &node); err != nil {
		return nil, err
	}
	return &node, decoder.ctx.Err()
}

// decodeGuardedYAMLNodeContext is the sole mutation-owned yaml.Unmarshal
// boundary. It is used for isolated resolver and renderer probes.
func decodeGuardedYAMLNodeContext(ctx context.Context, source []byte) (*yaml.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(source, &node); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateMutationYAMLGraphContext(ctx, &node); err != nil {
		return nil, err
	}
	return &node, ctx.Err()
}
