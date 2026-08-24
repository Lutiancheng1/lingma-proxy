package httpapi

// Embedded tool descriptions extracted verbatim from the QoderCN CLI
// (qoderclicn) tool registry. These back gateway-only capabilities that clients
// like Claude Code do not have natively (image search / image generation). The
// text is the model-facing description the CLI ships, reused so an advertised
// tool behaves the same way the CLI's does.

const webSearchToolDescription = `- Allows the model to search the web and use the results to inform responses
- Provides up-to-date information for current events and recent data
- Returns search result information formatted as search result blocks, including links as markdown hyperlinks
- Use this tool for accessing information beyond the model's knowledge cutoff
- Searches are performed automatically within a single API call
CRITICAL REQUIREMENT - You MUST follow this:
  - After answering the user's question, you MUST include a "Sources:" section at the end of your response
  - In the Sources section, list all relevant URLs from the search results as markdown hyperlinks: [Title](URL)
IMPORTANT - Use the correct year in search queries:
  - You MUST use the current year when searching for recent information, documentation, or current events.`

const imageSearchToolDescription = `Search the web for images and return structured metadata for candidate results. The tool DOES NOT download originals into the workspace; it returns result metadata such as title, imageUrl, and dimensions.
Use this for visual research and real-world asset sourcing, such as brand references, product imagery, places, events, or other existing/factual visuals.
After picking the result(s) you want, download the original image yourself with Bash curl or another suitable tool before using it as a local asset.
Use ImageGen only when the user needs a newly generated image rather than existing web imagery.`

const imageGenToolDescription = `Use this tool when you need to generate a high-fidelity image based on the prompt. The image is returned as a base64 data URL.
Provide a detailed English description of the image to generate. Available sizes (aspect ratio): 1024x1024 (1:1), 1536x1024 (3:2), 1024x1536 (2:3), 768x1024 (3:4), 1024x768 (4:3), 1024x1280 (4:5), 1280x1024 (5:4), 1024x1792 (9:16), 1792x1024 (16:9), 2560x1080 (21:9). Default is 1024x1024.`

// imageGenSizes is the set of sizes the gateway's generateImage accepts.
var imageGenSizes = []string{
	"1024x1024", "1536x1024", "1024x1536", "768x1024", "1024x768",
	"1024x1280", "1280x1024", "1024x1792", "1792x1024", "2560x1080",
}

// embeddedAnthropicTools returns the Anthropic tool definitions for the
// gateway-only capabilities, ready to advertise to the model.
func embeddedAnthropicTools() []any {
	return []any{
		map[string]any{
			"name":        "ImageSearch",
			"description": imageSearchToolDescription,
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "The image search query."},
					"count": map[string]any{"type": "integer", "minimum": 1, "maximum": 10, "description": "Number of image results (1-10, default 5)."},
				},
				"required": []any{"query"},
			},
		},
		map[string]any{
			"name":        "ImageGen",
			"description": imageGenToolDescription,
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string", "description": "Detailed description of the image to generate."},
					"size":   map[string]any{"type": "string", "enum": imageGenSizes, "description": "Image size (aspect ratio). Default 1024x1024."},
				},
				"required": []any{"prompt"},
			},
		},
	}
}
