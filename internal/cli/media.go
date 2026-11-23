package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"starry-cli/pkg/assets"
	"starry-cli/pkg/transport"
)

func uploadImageFile(ctx context.Context, client *transport.Client, token, source, key, purpose string) (map[string]any, error) {
	if purpose == "" {
		purpose = "content"
	}
	if !slices.Contains([]string{"avatar", "cover", "background", "content", "illustration"}, purpose) {
		return nil, errors.New("image purpose must be avatar, cover, background, content or illustration")
	}
	bytes, err := readFile(source, 100<<20, false)
	if err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp("", "starry-image-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	input, output := filepath.Join(stage, "source"), filepath.Join(stage, "prepared")
	if err := os.WriteFile(input, bytes, 0600); err != nil {
		return nil, err
	}
	if err := formatHost(ctx, "image", purpose, input, output); err != nil {
		return nil, err
	}
	return assets.UploadFile(ctx, client, token, output, key)
}
