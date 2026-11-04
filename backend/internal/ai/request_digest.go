package ai

import "context"

func jsonAttemptMessages(prompt string, encoded []byte, feedback string, images []Image) []Message {
	var content any = string(encoded) + feedback
	if len(images) > 0 {
		parts := []ContentPart{{Type: "text", Text: string(encoded) + feedback}}
		for _, image := range images {
			parts = append(parts, ContentPart{Type: "text", Text: image.Label}, ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: image.URL}})
		}
		content = parts
	}
	return []Message{{Role: "system", Content: prompt}, {Role: "user", Content: content}}
}

// JSONRequestDigest uses the exact first physical request builder shared with
// JSONWithOptions; auth headers/keys are never included in the digest payload.
func (client *Client) JSONRequestDigest(ctx context.Context, prompt string, input any, options JSONOptions) (string, error) {
	encoded, err := marshalNoEscape(input)
	if err != nil {
		return "", err
	}
	parts, err := buildChatRequest(ctx, client.Profile, jsonAttemptMessages(prompt, encoded, "", options.Images), options, nil)
	if err != nil {
		return "", err
	}
	return recipeDigest(parts.endpoint + "\n" + recipeDigest(string(parts.encoded))), nil
}
