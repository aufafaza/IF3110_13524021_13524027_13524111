package static

import "embed"

//go:embed htmx.min.js app.js app.css
var FS embed.FS
