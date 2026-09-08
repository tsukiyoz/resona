package ts3

import (
	"context"

	"github.com/tsukiyoz/resona/internal/iconcache"
)

// Bump this whenever thumbnail decoding, resizing, or output semantics change.
const iconTransformVersion = "png-catmullrom-256-v1"

func newCachedIconLoader(ctx context.Context, cache *iconcache.Cache, scope func() iconcache.Key,
	fetch func(context.Context, string) (string, error), publish func(string, string)) *iconLoader {
	return newIconLoader(ctx, func(ctx context.Context, id string) (string, error) {
		key := scope()
		key.IconID, key.TransformVersion = id, iconTransformVersion
		return cache.Reference(ctx, key, func(ctx context.Context) (string, error) { return fetch(ctx, id) })
	}, publish)
}
