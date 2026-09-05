// Package web embeds the evaluator's templates and static assets into the
// binary, so a deployment is a single file with no accompanying source
// tree (see docs/architektur.md, technology stack).
package web

import "embed"

//go:embed templates static
var FS embed.FS
