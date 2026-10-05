package api

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gofiber/fiber/v3"
)

func TestSPAHandler(t *testing.T) {
	ui := fstest.MapFS{
		"index.html":         {Data: []byte("<html>app</html>")},
		"assets/app-1.js":    {Data: []byte("console.log(1)")},
		"assets/style.css":   {Data: []byte("body{}")},
		"assets/geist.woff2": {Data: []byte("wOF2")},
	}
	app := fiber.New()
	app.Get("/*", spaHandler(ui))

	cases := []struct {
		path, wantBody, wantType string
		wantStatus               int
	}{
		{"/", "<html>app</html>", "text/html", 200},
		{"/instances", "<html>app</html>", "text/html", 200},
		{"/users/3/edit", "<html>app</html>", "text/html", 200},
		{"/assets/app-1.js", "console.log(1)", "text/javascript", 200},
		{"/assets/geist.woff2", "wOF2", "font/woff2", 200},
		{"/assets", "<html>app</html>", "text/html", 200},
		{"/assets/missing.js", "", "", 404},
		{"/api/v1/nope", "", "", 404},
		{"/../../etc/passwd", "", "", 200}, // cleaned to a route, served index
	}
	for _, tc := range cases {
		resp, err := app.Test(httptest.NewRequest("GET", tc.path, nil))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != tc.wantStatus {
			t.Errorf("%s: status %d, want %d", tc.path, resp.StatusCode, tc.wantStatus)
			continue
		}
		if tc.wantBody != "" && string(body) != tc.wantBody {
			t.Errorf("%s: body %q", tc.path, body)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, tc.wantType) {
			t.Errorf("%s: content type %q, want %s", tc.path, ct, tc.wantType)
		}
	}
}
