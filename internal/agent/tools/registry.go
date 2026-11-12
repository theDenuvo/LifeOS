// Package tools implements the LifeOS tool registry exposed to the assistant.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/theDenuvo/LifeOS/internal/application/assistant"
)

// Tool is one registered capability.
type Tool struct {
	Spec     assistant.ToolSpec
	ReadOnly bool
	Execute  func(ctx context.Context, arguments json.RawMessage) (any, error)
	Preview  func(arguments json.RawMessage) (string, error)
}

// Registry holds the tools available to the model.
type Registry struct {
	tools map[string]Tool
	order []string
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds a tool. Registering the same name twice replaces it.
func (r *Registry) Register(tool Tool) {
	if _, exists := r.tools[tool.Spec.Name]; !exists {
		r.order = append(r.order, tool.Spec.Name)
	}
	r.tools[tool.Spec.Name] = tool
}

// Specs returns the tool descriptions in registration order.
func (r *Registry) Specs() []assistant.ToolSpec {
	specs := make([]assistant.ToolSpec, 0, len(r.order))
	for _, name := range r.order {
		specs = append(specs, r.tools[name].Spec)
	}
	return specs
}

// IsReadOnly reports whether a tool only reads data.
func (r *Registry) IsReadOnly(name string) bool {
	tool, ok := r.tools[name]
	return ok && tool.ReadOnly
}

// Execute runs a tool.
func (r *Registry) Execute(ctx context.Context, name string, arguments json.RawMessage) (any, error) {
	tool, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", assistant.ErrToolNotFound, name)
	}
	return tool.Execute(ctx, arguments)
}

// Preview renders a human-readable description of a proposed mutation.
func (r *Registry) Preview(name string, arguments json.RawMessage) (string, error) {
	tool, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("%w: %s", assistant.ErrToolNotFound, name)
	}
	if tool.Preview == nil {
		return name, nil
	}
	return tool.Preview(arguments)
}
