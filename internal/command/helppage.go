package command

import (
	"bytes"
	_ "embed"
	"html"
	"html/template"
	"strings"
)

// helpFooter is the footer of codecheck.org.uk, copied on 2026-11-26: the
// site publishes it under CC BY-SA 4.0, as the footer itself says, not under
// this repository's licence. Unchanged but for the drawing program's metadata
// in its icons, which is gone, its links, which are absolute, and the "build"
// line after the FAQs, which is this page's own. It is a copy
// because the site has no file to load it from; take it again when the site's
// changes.
//
//go:embed helpfooter.html
var helpFooter string

// HelpPath is where the deployment serves the help page: the route, and what a
// reply links to.
const HelpPath = "/help"

// helpTemplate is the whole page, without scripts. Its stylesheets, logo and
// icon are the CODECHECK website's own, loaded from there so that the page
// follows the site; without them it is plainer and says the same. The style
// here is only what the site has no word for. One table per place, so that
// its columns line up from group to group.
var helpTemplate = func() *template.Template {
	page := template.Must(template.New("help").Funcs(template.FuncMap{
		"code":  inlineCode,
		"usage": breakableUsage,
	}).Parse(helpText))
	template.Must(page.New("footer").Parse(helpFooter))
	return page
}()

const helpText = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>{{.Mention}} commands | CODECHECK</title>
<link rel="stylesheet" href="https://codecheck.org.uk/assets/bootstrap.min.css">
<link rel="stylesheet" href="https://codecheck.org.uk/assets/main.css">
<link rel="stylesheet" href="https://codecheck.org.uk/assets/codecheck.css">
<link rel="shortcut icon" type="image/png" href="https://codecheck.org.uk/favicon.png">
<style>
.logo { display: block; width: 18rem; max-width: 100%; margin: 1.5rem 0; }
nav.places { display: flex; flex-wrap: wrap; gap: 1rem; margin-bottom: 1rem; }
nav.places a { flex: 1 1 18rem; border: 1px solid #e9ecef; border-left: .5rem solid var(--place); border-radius: .25rem; padding: .6rem .8rem; color: inherit; }
nav.places strong { display: block; color: var(--place); }
section { border-top: .4rem solid var(--place); margin-top: 3rem; }
.register { --place: #008033; }
.lists { --place: #b35c00; }
#site-footer { margin-top: 3rem; }
#content section h2 { color: var(--place); margin-top: 1rem; }
table { border-collapse: collapse; width: 100%; table-layout: fixed; margin-bottom: 1rem; }
th, td { border-bottom: 1px solid #e9ecef; padding: .4rem .8rem .4rem 0; text-align: left; vertical-align: top; }
.group th { border-bottom: 2px solid var(--place); padding-top: 1.5rem; font-size: 1.15rem; }
.command { width: 42%; }
.who { width: 18%; }
code { color: inherit; overflow-wrap: break-word; }
.where { display: block; font-size: .85em; color: #6c757d; }
@media (max-width: 40rem) {
  table, tbody, tr, th, td { display: block; }
  colgroup, thead { display: none; }
  tr { border-bottom: 1px solid #e9ecef; padding: .4rem 0; }
  td, .group th { padding: .1rem 0; }
  td { border: 0; }
  td.may::before { content: "For "; }
}
</style>
</head>
<body>
<header id="nav-header" class="border-dark border-left-0 border-right-0">
<div id="nav-container" class="container d-flex align-items-center">
<a class="h4 m-0 text-dark" href="https://codecheck.org.uk/">CODECHECK</a>
</div>
</header>
<main class="container" id="content">
<a href="https://codecheck.org.uk/"><img class="logo" src="https://codecheck.org.uk/img/codecheck_logo.svg" alt="CODECHECK logo"></a>
<h1><code>{{.Mention}}</code> commands</h1>
<p><a href="https://github.com/codecheckers/chekhov">The CODECHECK register bot</a>. Write a command on the <strong>first line</strong>
of a comment. More may follow, each on a line of its own beginning with
<code>{{.Mention}}</code>, up to {{.Most}} in one comment.</p>
<p>Each command works in <strong>one place only</strong>:</p>
<nav class="places">{{range .Places}}
<a class="{{.ID}}" href="#{{.ID}}"><strong>{{.Title}}</strong>issues of {{.Repository}}</a>{{end}}
</nav>
{{range .Places}}
<section class="{{.ID}}" id="{{.ID}}">
<h2>{{.Title}}</h2>
<p>Comment in an issue of
<a href="https://github.com/{{.Repository}}/issues">{{.Repository}}</a>.
{{if .RegistrationLabel}}People register as codecheckers there.
A registration is an issue labelled <code>{{.RegistrationLabel}}</code>,
and the bot answers it as soon as it is opened.{{else}}Each CODECHECK has its own issue there.{{end}}</p>
<table>
<colgroup><col class="command"><col><col class="who"></colgroup>
<thead><tr><th>Command</th><th>What it does</th><th>Who may run it</th></tr></thead>
{{range .Sections}}<tbody>
<tr class="group"><th colspan="3">{{.Title}}</th></tr>
{{range .Commands}}<tr><td><code>{{$.Mention}} {{usage .Usage}}</code></td><td>{{code .Summary}}{{if .Lists}}<span class="where">In {{.Where}}</span>{{end}}</td><td class="may">{{.Who}}</td></tr>
{{end}}</tbody>
{{end}}</table>
</section>
{{end}}
</main>
{{template "footer" .}}</body>
</html>
{{define "build"}}<p class="text-secondary"><small>{{.Mention}} version {{.Deployment.Version}} ·
<a href="https://github.com/codecheckers/chekhov">source and documentation</a></small></p>{{end}}`

// A helpPlace is one repository the bot answers on, and what it answers there.
type helpPlace struct {
	ID, Title, Repository string
	Sections              []section
	// RegistrationLabel is set on the lists alone.
	RegistrationLabel string
}

// HelpPage is the registry as a web page, so that looking a command up does
// not take a comment on an issue.
//
// A page has no asker whose roles could filter it, so unlike Listing it shows
// every command that is not hidden, and says who may run each. It is arranged
// by place first, because the register and the codechecker lists answer
// different commands; the lists are left out where the deployment does not
// work on them. About the machine it says what /healthz says in production.
func HelpPage(d Deployment) ([]byte, error) {
	var register, lists []Definition
	for _, definition := range registry {
		switch {
		case definition.Hidden:
		case definition.Lists:
			lists = append(lists, definition)
		default:
			register = append(register, definition)
		}
	}
	places := []helpPlace{{ID: "register", Title: "On the register", Repository: d.Register, Sections: byGroup(register)}}
	if d.Lists != "" {
		places = append(places, helpPlace{ID: "lists", Title: "On the codechecker lists", Repository: d.Lists,
			Sections: byGroup(lists), RegistrationLabel: d.RegistrationLabel})
	}

	var page bytes.Buffer
	err := helpTemplate.Execute(&page, map[string]any{
		"Deployment": d,
		"Mention":    Bot,
		"Most":       MostCommands,
		"Places":     places,
	})
	return page.Bytes(), err
}

// inlineCode renders the backticks of a summary as GitHub renders them in the
// listing, escaping everything.
func inlineCode(text string) template.HTML {
	var out strings.Builder
	for i, part := range strings.Split(text, "`") {
		part = html.EscapeString(part)
		if i%2 == 1 {
			part = "<code>" + part + "</code>"
		}
		out.WriteString(part)
	}
	return template.HTML(out.String())
}

// breakableUsage lets a long usage wrap after a `|` between alternatives
// rather than in the middle of a word.
func breakableUsage(usage string) template.HTML {
	return template.HTML(strings.ReplaceAll(html.EscapeString(usage), "|", "|<wbr>"))
}
