package api

import (
	"errors"
	"io/fs"
	"mime"
	"path"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// spaHandler serves files from ui and falls back to index.html for any other
// path, so client-side routes like /instances survive a page reload.
func spaHandler(ui fs.FS) fiber.Handler {
	return func(c fiber.Ctx) error {
		if strings.HasPrefix(c.Path(), "/api/") {
			return fiber.ErrNotFound
		}
		if ui == nil {
			return c.Status(fiber.StatusNotFound).SendString("web UI not built; run `pnpm build` in web/")
		}
		name := strings.TrimPrefix(path.Clean("/"+c.Path()), "/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(ui, name); err == nil && !st.IsDir() {
				b, err := fs.ReadFile(ui, name)
				if err != nil {
					return err
				}
				c.Set(fiber.HeaderContentType, contentType(name))
				if strings.HasPrefix(name, "assets/") {
					// Vite puts a content hash in asset file names.
					c.Set(fiber.HeaderCacheControl, "public, max-age=31536000, immutable")
				}
				return c.Send(b)
			}
			// A missing file with an extension is a real 404, not a route.
			if path.Ext(name) != "" {
				return fiber.ErrNotFound
			}
		}
		index, err := fs.ReadFile(ui, "index.html")
		if errors.Is(err, fs.ErrNotExist) {
			return c.Status(fiber.StatusNotFound).SendString("web UI not built; run `pnpm build` in web/")
		}
		if err != nil {
			return err
		}
		c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
		c.Set(fiber.HeaderCacheControl, "no-cache")
		return c.Send(index)
	}
}

// Types Go's built-in table lacks; minimal images have no /etc/mime.types.
var extraTypes = map[string]string{
	".woff2": "font/woff2",
	".woff":  "font/woff",
	".ttf":   "font/ttf",
	".ico":   "image/x-icon",
}

func contentType(name string) string {
	if t, ok := extraTypes[path.Ext(name)]; ok {
		return t
	}
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return fiber.MIMEOctetStream
}
