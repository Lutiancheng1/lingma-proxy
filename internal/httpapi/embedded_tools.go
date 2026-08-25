package httpapi

import "lingma-ipc-proxy/internal/toolemulation"

// Embedded tool descriptions extracted verbatim from the QoderCN CLI
// (qoderclicn) tool registry. These back gateway-only capabilities that clients
// like Claude Code / OpenAI clients do not have natively (web search / image
// search). The text is the model-facing description the CLI ships, reused so an
// advertised tool behaves the same way the CLI's does. The proxy advertises
// these as "server tools": the model decides whether to call them, and the
// proxy executes them server-side (see media_tools.go / openai_server_tools.go).

const webSearchToolDescription = `- Allows the model to search the web and use the results to inform responses
- Provides up-to-date information for current events and recent data
- Returns search result information formatted as search result blocks, including links as markdown hyperlinks
- Use this tool for accessing information beyond the model's knowledge cutoff
CRITICAL REQUIREMENT - You MUST follow this:
  - After answering the user's question, you MUST include a "Sources:" section at the end of your response
  - In the Sources section, list all relevant URLs from the search results as markdown hyperlinks: [Title](URL)
IMPORTANT - Use the correct year in search queries:
  - You MUST use the current year when searching for recent information, documentation, or current events.`

const imageSearchToolDescription = `Search the web for images and return structured metadata for candidate results. The tool DOES NOT download originals into the workspace; it returns result metadata such as title, imageUrl, and dimensions.
Use this for visual research and real-world asset sourcing, such as brand references, product imagery, places, events, or other existing/factual visuals.
After picking the result(s) you want, download the original image yourself with Bash curl or another suitable tool before using it as a local asset.`

const textPolishToolDescription = `Polish raw or unpunctuated text: add correct punctuation, fix capitalization and spacing, and clean up spoken-language artifacts, WITHOUT changing the wording, meaning, or language.
Ideal for cleaning up speech-to-text transcriptions, rough dictation, or messy pasted text before using it.
Returns only the cleaned text. It does NOT rewrite, summarize, translate, or answer the content.`

// serverToolSpec is the canonical definition of a proxy-executed "server tool":
// a gateway-only capability the proxy advertises to the model and runs
// server-side. One spec generates both the Anthropic tool def (a raw map with
// input_schema) and the OpenAI/service ToolDef, so the two API surfaces stay in
// sync from a single source.
type serverToolSpec struct {
	name        string
	description string
	schema      map[string]any
}

var webSearchSpec = serverToolSpec{
	name:        "web_search",
	description: webSearchToolDescription,
	schema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "The web search query."},
		},
		"required": []any{"query"},
	},
}

var imageSearchSpec = serverToolSpec{
	name:        "ImageSearch",
	description: imageSearchToolDescription,
	schema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "The image search query."},
			"count": map[string]any{"type": "integer", "minimum": 1, "maximum": 10, "description": "Number of image results (1-10, default 5)."},
		},
		"required": []any{"query"},
	},
}

var textPolishSpec = serverToolSpec{
	name:        "TextPolish",
	description: textPolishToolDescription,
	schema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{"type": "string", "description": "The raw text to polish (add punctuation, fix casing/spacing)."},
		},
		"required": []any{"text"},
	},
}

// anthropicDef renders the spec as an Anthropic tool definition (name +
// description + input_schema), ready to append to a request's tools list.
func (t serverToolSpec) anthropicDef() map[string]any {
	return map[string]any{
		"name":         t.name,
		"description":  t.description,
		"input_schema": t.schema,
	}
}

// toolDef renders the spec as a service-layer ToolDef, used on the OpenAI path
// where the agentic loop operates on the normalized service.ChatRequest.
func (t serverToolSpec) toolDef() toolemulation.ToolDef {
	return toolemulation.ToolDef{
		Name:        t.name,
		Description: t.description,
		InputSchema: t.schema,
	}
}
