// Command overlay-web serves a browser preview of the provisioning
// overlay so image parameters can be iterated on without running the
// TUI. Renders all 14 phases side-by-side as HTML with inline CSS
// converted from the overlay's truecolor ANSI output. Sliders in the
// header let you tweak the main image knobs; reload after changing
// Go code.
//
//	make overlay-web       # starts http://localhost:8080/
package main

import (
	"flag"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/elliottregan/cspace/internal/overlay"
	"github.com/elliottregan/cspace/internal/planets"
	"github.com/elliottregan/cspace/internal/provision"
)

var addr = flag.String("addr", ":8080", "listen address")

var planetNames = []string{
	"mercury", "venus", "earth", "mars",
	"jupiter", "saturn", "uranus", "neptune",
}

func main() {
	flag.Parse()
	http.HandleFunc("/", handleIndex)
	log.Printf("overlay preview at http://localhost%s/", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

type frameData struct {
	Phase int
	Label string
	State string
	HTML  template.HTML
}

type indexData struct {
	Planets []string
	Planet  string
	Total   int
	Frames  []frameData
	Opts    overlay.RenderOptions
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	planet := q.Get("planet")
	if planet == "" {
		planet = "mercury"
	}

	opts := overlay.DefaultRenderOptions()
	if v := q.Get("blockSize"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.MaxBlockSize = n
		}
	}
	if v := q.Get("halo"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			opts.HaloThreshold = f
		}
	}
	if v := q.Get("texStart"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			opts.TextureStart = f
		}
	}
	if v := q.Get("texDensity"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			opts.TextureDensity = f
		}
	}
	if v := q.Get("texContrast"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			opts.TextureContrast = f
		}
	}

	p := planets.MustGet(planet)
	shape := planets.GetShape(planet)
	total := len(provision.Phases)

	frames := make([]frameData, 0, total)
	for phase := 1; phase <= total; phase++ {
		art := overlay.RenderPlanetWith(shape, p, phase, total, opts)
		label := provision.Phases[phase-1]
		frames = append(frames, frameData{
			Phase: phase,
			Label: label,
			State: overlay.SciFiLabelFor(label),
			HTML:  template.HTML(ansiToHTML(art)),
		})
	}

	data := indexData{
		Planets: planetNames,
		Planet:  planet,
		Total:   total,
		Frames:  frames,
		Opts:    opts,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tpl.Execute(w, data); err != nil {
		log.Printf("template execute: %v", err)
	}
}

var ansiRegex = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// ansiToHTML converts the overlay's truecolor ANSI output to an HTML
// fragment of <span style="color:#..; background:#..">…</span> runs.
// Only handles SGR reset (0) and truecolor (38;2;R;G;B / 48;2;R;G;B),
// which is all the overlay emits.
func ansiToHTML(s string) string {
	var out strings.Builder
	fg, bg := "", ""
	spanOpen := false

	openSpan := func() {
		if spanOpen {
			return
		}
		style := ""
		if fg != "" {
			style += "color:" + fg + ";"
		}
		if bg != "" {
			style += "background:" + bg + ";"
		}
		if style == "" {
			return
		}
		out.WriteString(`<span style="`)
		out.WriteString(style)
		out.WriteString(`">`)
		spanOpen = true
	}
	closeSpan := func() {
		if spanOpen {
			out.WriteString(`</span>`)
			spanOpen = false
		}
	}
	writeText := func(t string) {
		if t == "" {
			return
		}
		openSpan()
		out.WriteString(html.EscapeString(t))
	}

	parseSGR := func(codes string) {
		if codes == "" || codes == "0" {
			fg, bg = "", ""
			return
		}
		parts := strings.Split(codes, ";")
		for i := 0; i < len(parts); i++ {
			switch parts[i] {
			case "0":
				fg, bg = "", ""
			case "38":
				if i+4 < len(parts) && parts[i+1] == "2" {
					r, _ := strconv.Atoi(parts[i+2])
					g, _ := strconv.Atoi(parts[i+3])
					b, _ := strconv.Atoi(parts[i+4])
					fg = fmt.Sprintf("#%02x%02x%02x", r, g, b)
					i += 4
				}
			case "48":
				if i+4 < len(parts) && parts[i+1] == "2" {
					r, _ := strconv.Atoi(parts[i+2])
					g, _ := strconv.Atoi(parts[i+3])
					b, _ := strconv.Atoi(parts[i+4])
					bg = fmt.Sprintf("#%02x%02x%02x", r, g, b)
					i += 4
				}
			}
		}
	}

	i := 0
	for i < len(s) {
		loc := ansiRegex.FindStringIndex(s[i:])
		if loc == nil {
			writeText(s[i:])
			break
		}
		writeText(s[i : i+loc[0]])
		closeSpan()
		codes := s[i+loc[0]+2 : i+loc[1]-1]
		parseSGR(codes)
		i += loc[1]
	}
	closeSpan()
	return out.String()
}

var tpl = template.Must(template.New("index").Parse(pageTemplate))

const pageTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Overlay preview · {{.Planet}}</title>
<style>
  :root {
    --bg: #101010;
    --panel: #000;
    --ink: #d4d4d4;
    --dim: #888;
    --border: #2a2a2a;
    --accent: #4fc3f7;
  }
  * { box-sizing: border-box; }
  html, body { margin: 0; padding: 0; }
  body {
    background: var(--bg);
    color: var(--ink);
    font-family: 'JetBrains Mono', 'SF Mono', Menlo, Consolas, monospace;
    font-size: 13px;
  }
  header.controls {
    position: sticky;
    top: 0;
    z-index: 10;
    background: #181818;
    border-bottom: 1px solid var(--border);
    padding: 12px 20px;
    display: flex;
    flex-wrap: wrap;
    gap: 18px;
    align-items: center;
  }
  header h1 {
    margin: 0 12px 0 0;
    font-size: 13px;
    font-weight: 600;
    letter-spacing: 0.1em;
    color: var(--ink);
  }
  header form { display: contents; }
  header label {
    display: flex;
    gap: 6px;
    align-items: center;
    color: var(--dim);
    font-size: 11px;
    letter-spacing: 0.05em;
  }
  header label span.val {
    color: var(--ink);
    min-width: 36px;
    text-align: right;
  }
  header select, header input[type=range] {
    background: #111;
    color: var(--ink);
    border: 1px solid #333;
    font: inherit;
  }
  header select { padding: 2px 6px; }
  header input[type=range] { width: 110px; }
  header button {
    background: #111;
    color: var(--ink);
    border: 1px solid #333;
    font: inherit;
    padding: 3px 10px;
    cursor: pointer;
  }
  header button:hover { border-color: var(--accent); }

  .grid {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 20px;
    padding: 20px;
  }
  @media (min-width: 1400px) { .grid { grid-template-columns: repeat(3, 1fr); } }
  @media (min-width: 1900px) { .grid { grid-template-columns: repeat(4, 1fr); } }

  .frame {
    background: var(--panel);
    border: 1px solid var(--border);
    padding: 14px 18px;
  }
  .frame .caption {
    font-size: 11px;
    letter-spacing: 0.08em;
    color: var(--dim);
    margin-bottom: 10px;
    display: flex;
    justify-content: space-between;
  }
  .frame .caption .state {
    color: var(--ink);
    font-weight: 600;
  }
  pre.art {
    margin: 0;
    font-family: inherit;
    font-size: 13px;
    line-height: 1.0;
    letter-spacing: 0;
    white-space: pre;
    color: #fff;
  }
</style>
</head>
<body>
<header class="controls">
  <h1>OVERLAY PREVIEW</h1>
  <form action="/" method="get">
    <label>PLANET
      <select name="planet" onchange="this.form.submit()">
        {{range .Planets}}<option value="{{.}}"{{if eq . $.Planet}} selected{{end}}>{{.}}</option>{{end}}
      </select>
    </label>
    <label>BLOCK <span class="val" id="v-blockSize">{{.Opts.MaxBlockSize}}</span>
      <input type="range" name="blockSize" min="1" max="16" step="1" value="{{.Opts.MaxBlockSize}}"
        oninput="document.getElementById('v-blockSize').textContent=this.value" onchange="this.form.submit()">
    </label>
    <label>HALO <span class="val" id="v-halo">{{printf "%.2f" .Opts.HaloThreshold}}</span>
      <input type="range" name="halo" min="0" max="0.5" step="0.01" value="{{printf "%.2f" .Opts.HaloThreshold}}"
        oninput="document.getElementById('v-halo').textContent=this.value" onchange="this.form.submit()">
    </label>
    <label>TEX.START <span class="val" id="v-texStart">{{printf "%.2f" .Opts.TextureStart}}</span>
      <input type="range" name="texStart" min="0" max="1" step="0.01" value="{{printf "%.2f" .Opts.TextureStart}}"
        oninput="document.getElementById('v-texStart').textContent=this.value" onchange="this.form.submit()">
    </label>
    <label>TEX.DENS <span class="val" id="v-texDensity">{{printf "%.2f" .Opts.TextureDensity}}</span>
      <input type="range" name="texDensity" min="0" max="1" step="0.01" value="{{printf "%.2f" .Opts.TextureDensity}}"
        oninput="document.getElementById('v-texDensity').textContent=this.value" onchange="this.form.submit()">
    </label>
    <label>TEX.CON <span class="val" id="v-texContrast">{{printf "%.2f" .Opts.TextureContrast}}</span>
      <input type="range" name="texContrast" min="0" max="0.5" step="0.01" value="{{printf "%.2f" .Opts.TextureContrast}}"
        oninput="document.getElementById('v-texContrast').textContent=this.value" onchange="this.form.submit()">
    </label>
  </form>
  <button onclick="location.reload()">RELOAD</button>
</header>

<div class="grid">
  {{range .Frames}}
  <div class="frame">
    <div class="caption">
      <span>PH {{printf "%02d" .Phase}} / {{$.Total}} &middot; {{.Label}}</span>
      <span class="state">{{.State}}</span>
    </div>
    <pre class="art">{{.HTML}}</pre>
  </div>
  {{end}}
</div>
</body>
</html>
`
