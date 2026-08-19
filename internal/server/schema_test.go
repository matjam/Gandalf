package server

import (
	"strings"
	"testing"
)

// TestEveryToolParameterIsDescribed is the guard for what a model can find out
// about a parameter without asking.
//
// A tool description says what the tool is for; it does not say whether a field
// wants a ref or a title, a singular or a plural, one commit or two. That
// answer can only be in the schema, and an undescribed field is one a model has
// to guess at. The omissions this test was written for were all in tools added
// after the ones that got it right, which is exactly the drift a convention
// does not catch.
func TestEveryToolParameterIsDescribed(t *testing.T) {
	h := newHarness(t)

	res, err := h.client.ListTools(h.context, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("no tools registered")
	}

	for _, tool := range res.Tools {
		schema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Errorf("%s: input schema is %T, want an object", tool.Name, tool.InputSchema)
			continue
		}

		// A tool taking no arguments has no properties, which is not a gap.
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			continue
		}

		for name, raw := range properties {
			property, ok := raw.(map[string]any)
			if !ok {
				t.Errorf("%s.%s: property is %T, want an object", tool.Name, name, raw)
				continue
			}

			description, _ := property["description"].(string)
			if strings.TrimSpace(description) == "" {
				t.Errorf("%s.%s has no description", tool.Name, name)
			}
		}
	}
}
