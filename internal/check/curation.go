package check

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/codecheckers/chekhov/internal/rules"
)

// `check zenodo` (codecheckers/chekhov#26): the certificate's record held
// against the curation policy of the CODECHECK community on Zenodo,
// https://zenodo.org/communities/codecheck/curation-policy, and against the
// codecheck.yml.
//
// The policy items follow zenodo_policy_check() in the R package
// (../codecheck/R/zenodo.R), which audits published records for the
// register's `make zenodo_check`; the differences are deliberate and named
// where they are. The register rules about the record, CC-REP-*, are the
// ones `check report` runs, asked of the record read here rather than of the
// one the report DOI names: that is what lets a draft be audited before
// anything is published.
//
// A draft is read through a share link, whose secret is a `token` query
// parameter (InvenioRDM's link sharing). The secret is a credential somebody
// handed to the issue, so it never leaves this file: errors are written
// without the URL, and ZenodoTarget.String leaves it out.

// zenodoCommunity is the CODECHECK community on Zenodo, by the identifier
// InvenioRDM lists it under and by its slug. The R package keeps the same
// pair as zenodo_codecheck_community_id. The sandbox has a community of the
// same slug, made for the integration test, under an identifier of its own.
const (
	zenodoCommunityID        = "505e49f2-de4f-4905-9666-36c7303e497d"
	zenodoSandboxCommunityID = "1a911ac4-cf94-4b20-b488-6b8b23ce38d5"
	zenodoCommunitySlug      = "codecheck"
)

// What the policy asks for in so many words.
const (
	policyPublisher    = "CODECHECK Community on Zenodo"
	policyResourceType = "publication-report"
	ccBy4              = "cc-by-4.0"
	certificatesURL    = "cdchck.science/register/certs/"
)

// invenioMediaType asks Zenodo for InvenioRDM's own representation of a
// record. The default one leaves out the publisher and the community, and a
// draft has no other.
const invenioMediaType = "application/vnd.inveniordm.v1+json"

// A ZenodoTarget is the record an audit reads.
type ZenodoTarget struct {
	ID      string
	Sandbox bool
	// Token is a share link's secret. Never shown, see String.
	Token string
	// Preview says the link was to a draft's preview, which is where to
	// look first.
	Preview bool
	// DOI is the DOI the target was named by, empty when it was named by a
	// link.
	DOI string
}

// String is the record as a reply may name it: never with the token.
func (t ZenodoTarget) String() string {
	if t.DOI != "" {
		return t.DOI
	}
	host := "zenodo.org"
	if t.Sandbox {
		host = "sandbox.zenodo.org"
	}
	return "https://" + host + "/records/" + t.ID
}

// ErrOutOfScope is a record on a platform the community policy is not about.
var ErrOutOfScope = errors.New("out of scope")

// OutOfScopeError is ErrOutOfScope naming the platform the record is on.
type OutOfScopeError struct{ Platform string }

func (e *OutOfScopeError) Error() string        { return "out of scope: " + e.Platform }
func (e *OutOfScopeError) Is(target error) bool { return target == ErrOutOfScope }

var (
	zenodoDOI = regexp.MustCompile(`(?i)10\.(5281|5072)/zenodo\.([0-9]+)`)
	// zenodoPath is a record's page or its API, the old singular route and
	// the deposit form included: whichever of them somebody copies.
	zenodoPath  = regexp.MustCompile(`^/(?:api/)?(?:records?|uploads|deposit)/([0-9]+)(?:/draft)?/?$`)
	osfDOI      = regexp.MustCompile(`(?i)10\.17605/osf\.io/`)
	researchDOI = regexp.MustCompile(`(?i)10\.53962/`)
)

// ParseZenodoTarget reads a DOI, a record's link, or a share link. A record
// on OSF or ResearchEquals is an *OutOfScopeError, which is ErrOutOfScope; anything else that is not a Zenodo record is an error.
func ParseZenodoTarget(word string) (ZenodoTarget, error) {
	word = strings.Trim(strings.TrimSpace(word), "<>")
	lower := strings.ToLower(word)
	switch {
	case osfDOI.MatchString(word) || strings.Contains(lower, "osf.io/"):
		return ZenodoTarget{}, &OutOfScopeError{"OSF"}
	case researchDOI.MatchString(word) || strings.Contains(lower, "researchequals.com"):
		return ZenodoTarget{}, &OutOfScopeError{"ResearchEquals"}
	}

	if link, err := url.Parse(word); err == nil && link.Host != "" {
		host := strings.TrimPrefix(strings.ToLower(link.Hostname()), "www.")
		if host == "zenodo.org" || host == "sandbox.zenodo.org" {
			// Any other path may still carry a DOI, as Zenodo's own
			// zenodo.org/doi/10.5281/zenodo.N links do.
			match := zenodoPath.FindStringSubmatch(link.Path)
			if match == nil && !zenodoDOI.MatchString(link.Path) {
				return ZenodoTarget{}, errors.New("not the link to a record")
			}
			if match != nil {
				query := link.Query()
				return ZenodoTarget{ID: match[1], Sandbox: host == "sandbox.zenodo.org",
					Token:   query.Get("token"),
					Preview: query.Get("preview") == "1" || strings.HasSuffix(strings.TrimSuffix(link.Path, "/"), "/draft"),
				}, nil
			}
		}
	}
	if match := zenodoDOI.FindStringSubmatch(word); match != nil {
		return ZenodoTarget{ID: match[2], Sandbox: match[1] == "5072", DOI: match[0]}, nil
	}
	return ZenodoTarget{}, errors.New("not a Zenodo DOI or record link")
}

// invenioRecord is a record or a draft as InvenioRDM writes it, as much of
// it as the policy is about.
type invenioRecord struct {
	ID      string `json:"id"`
	IsDraft bool   `json:"is_draft"`
	// IsPublished, on a draft, says it is an edit of a published record.
	IsPublished bool `json:"is_published"`
	PIDs        struct {
		DOI struct {
			Identifier string `json:"identifier"`
		} `json:"doi"`
	} `json:"pids"`
	Parent struct {
		PIDs struct {
			DOI struct {
				Identifier string `json:"identifier"`
			} `json:"doi"`
		} `json:"pids"`
		Communities struct {
			IDs     []string         `json:"ids"`
			Entries []communityEntry `json:"entries"`
		} `json:"communities"`
		// Review is a draft's request to join a community, which is how a
		// draft is "submitted to the community" before it is in it.
		Review *struct {
			Status   string `json:"status"`
			Receiver struct {
				Community string `json:"community"`
			} `json:"receiver"`
		} `json:"review"`
	} `json:"parent"`
	Versions struct {
		Index    int  `json:"index"`
		IsLatest bool `json:"is_latest"`
	} `json:"versions"`
	Files    invenioFiles `json:"files"`
	Metadata struct {
		Title        string        `json:"title"`
		Description  string        `json:"description"`
		Publisher    string        `json:"publisher"`
		ResourceType zenodoRight   `json:"resource_type"`
		Languages    []zenodoRight `json:"languages"`
		Rights       []zenodoRight `json:"rights"`
		Creators     []struct {
			PersonOrOrg struct {
				Name        string `json:"name"`
				Identifiers []struct {
					Identifier string `json:"identifier"`
					Scheme     string `json:"scheme"`
				} `json:"identifiers"`
			} `json:"person_or_org"`
		} `json:"creators"`
		RelatedIdentifiers []struct {
			Identifier   string `json:"identifier"`
			Scheme       string `json:"scheme"`
			RelationType struct {
				ID string `json:"id"`
			} `json:"relation_type"`
		} `json:"related_identifiers"`
		Identifiers []zenodoIdentifier `json:"identifiers"`
	} `json:"metadata"`
}

// communityEntry is one community a record is in.
type communityEntry struct {
	Slug string `json:"slug"`
}

// invenioFiles are a record's files, keyed by name, and the order the
// depositor gave them, which is empty unless they gave one. A draft's
// listing answers in the same shape with the entries as a list, see files.
type invenioFiles struct {
	Order   []string              `json:"order"`
	Entries map[string]zenodoFile `json:"entries"`
}

// names are the record's file names, in the depositor's order where there is
// one and by name where there is not.
func (f invenioFiles) names() []string {
	names := slices.Clone(f.Order)
	var rest []string
	for key := range f.Entries {
		if !slices.Contains(names, key) {
			rest = append(rest, key)
		}
	}
	slices.Sort(rest)
	return append(names, rest...)
}

// asRecord is the record in the shape the CC-REP rules read, so that the
// rules `check report` runs on a published record judge a draft by the same
// code.
func (r invenioRecord) asRecord() zenodoRecord {
	record := zenodoRecord{DOI: r.PIDs.DOI.Identifier, ConceptDOI: r.Parent.PIDs.DOI.Identifier,
		Title: r.Metadata.Title, draft: r.IsDraft}
	record.Metadata.Title = r.Metadata.Title
	for _, name := range r.Files.names() {
		record.Files = append(record.Files, zenodoFile{Key: name})
	}
	record.Metadata.Rights = r.Metadata.Rights
	for _, creator := range r.Metadata.Creators {
		orcid := ""
		for _, identifier := range creator.PersonOrOrg.Identifiers {
			if strings.EqualFold(identifier.Scheme, "orcid") {
				orcid = identifier.Identifier
			}
		}
		record.Metadata.Creators = append(record.Metadata.Creators, zenodoCreator{creator.PersonOrOrg.Name, orcid})
	}
	// InvenioRDM counts versions from one and does not say how many there
	// are; the default representation counted from zero.
	if !r.IsDraft {
		record.Metadata.Relations.Version = []zenodoVersion{{Index: r.Versions.Index - 1, IsLast: r.Versions.IsLatest}}
	}
	return record
}

// readZenodo reads the record a target names: with a share link the draft
// first, because a share link is mostly how a draft is shown, and the
// published record otherwise.
func (s *Services) readZenodo(ctx context.Context, target ZenodoTarget) (invenioRecord, error) {
	api := s.Zenodo
	if target.Sandbox {
		api = s.ZenodoSandbox
	}
	query := ""
	if target.Token != "" {
		query = "?token=" + url.QueryEscape(target.Token)
	}
	header := map[string]string{"Accept": invenioMediaType}
	published := fmt.Sprintf("%s/records/%s", api, target.ID)
	draft := published + "/draft"
	order := []string{published}
	if target.Preview || target.Token != "" {
		order = []string{draft, published}
	}

	var err error
	for _, at := range order {
		var record invenioRecord
		err = s.getJSONContext(ctx, at+query, header, &record)
		switch {
		case err == nil && at == draft && record.IsPublished:
			// An edit in progress of a published record: the record is
			// what was published, not the edit.
			continue
		case err == nil && at == draft && len(record.Files.Entries) == 0:
			// A draft's files are not in the draft: they are listed beside
			// it. A listing that cannot be read is the answer, rather than
			// the published record that a draft does not have yet.
			record.Files, err = s.draftFiles(ctx, draft+"/files"+query, header)
			return record, withoutToken(err, target)
		case err == nil:
			return record, nil
		}
		if !IsNotFound(err) && !isRefused(err) {
			break
		}
	}
	return invenioRecord{}, withoutToken(err, target)
}

// draftFiles reads a draft's files, which InvenioRDM lists as a list rather
// than by name.
func (s *Services) draftFiles(ctx context.Context, at string, header map[string]string) (invenioFiles, error) {
	var listing struct {
		Entries []zenodoFile `json:"entries"`
	}
	files := invenioFiles{Entries: map[string]zenodoFile{}}
	if err := s.getJSONContext(ctx, at, header, &listing); err != nil {
		return files, err
	}
	for _, entry := range listing.Entries {
		files.Order = append(files.Order, entry.Key)
		files.Entries[entry.Key] = entry
	}
	return files, nil
}

// isRefused is a 401 or a 403: a restricted record, or a draft without the
// token.
func isRefused(err error) bool {
	var status *StatusError
	return errors.As(err, &status) && (status.Status == 401 || status.Status == 403)
}

// withoutToken is an error with the URL taken out, which is where the token
// would be: a StatusError and Go's own url.Error both quote it.
func withoutToken(err error, target ZenodoTarget) error {
	var status *StatusError
	var link *url.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &status):
		// Still a StatusError, so that a 404 is still a 404 to the caller.
		return &StatusError{URL: "Zenodo", Status: status.Status}
	case errors.As(err, &link):
		err = link.Err
	}
	if target.Token == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), target.Token, "…"))
}

// A PolicyItem is one thing the curation policy asks of a record.
type PolicyItem struct {
	What   string
	Status Status
	Detail string
}

// A ZenodoAudit is the answer to `check zenodo`.
type ZenodoAudit struct {
	// Record is what was read, as a reply may name it.
	Record string
	// Draft says the record is an unpublished draft.
	Draft bool
	// Shared says it was read with a share link's token.
	Shared bool
	// Problem is why the record could not be read; the rest is empty then.
	Problem string
	Policy  []PolicyItem
	// Rules are the CC-REP rules on the record that the policy list does not
	// show already, and RulesSummary counts them, so that a rule is counted
	// once, where it is shown.
	Rules        []RuleResult
	RulesSummary string
}

// AuditZenodo reads a record and holds it against the policy and, when there
// is one, the codecheck.yml in configuration, whose Config is empty
// otherwise.
func AuditZenodo(ctx context.Context, target ZenodoTarget, configuration Context, services *Services) ZenodoAudit {
	audit := ZenodoAudit{Record: target.String(), Shared: target.Token != ""}
	if !services.Enabled() {
		audit.Problem = "reading a Zenodo record needs the external services, and they are not enabled"
		return audit
	}
	record, err := services.readZenodo(ctx, target)
	if err != nil {
		audit.Problem = fmt.Sprintf("I could not read the record: %s", err)
		if IsNotFound(err) || isRefused(err) {
			audit.Problem += ". A draft or a restricted record needs a share link, " +
				"made under Share on the record's page"
		}
		return audit
	}
	audit.Draft = record.IsDraft
	// A draft's DOI is only reserved and does not resolve yet, so a draft is
	// named as it was asked for.
	if record.PIDs.DOI.Identifier != "" && target.DOI == "" && !record.IsDraft {
		audit.Record = record.PIDs.DOI.Identifier
	}

	reported := sameRecord(configuration.Config.Report, record)
	asRecord := record.asRecord()
	configuration.Services = services
	configuration.zenodo = &asRecord
	// The rules about the report DOI judge the one the codecheck.yml names,
	// and without one, the one asked about.
	if strings.TrimSpace(configuration.Config.Report) == "" {
		configuration.Config.Report = audit.Record
	}
	// Not SpecVersion, which would date a file that names no version: the
	// rules asked here are the same in every version that has them.
	version := SpecVersionNamed(configuration.Config.Version)
	if version == "" {
		version = rules.Newest()
	}
	report, err := RunPart(configuration, version, false, "report")
	if err != nil {
		audit.Problem = err.Error()
		return audit
	}

	byRule := map[string]RuleResult{}
	for _, result := range report.Results {
		byRule[result.Rule.ID] = result
	}
	certificate := strings.TrimSpace(configuration.Config.Certificate)
	if certificate == "" {
		certificate = certificateIDIn.FindString(record.Metadata.Title)
	}
	// The description is HTML, so only whether there is one is said.
	description := ""
	if strings.TrimSpace(record.Metadata.Description) != "" {
		description = "present"
	}
	// The policy items a rule decides, shown with the policy and not again
	// beneath it.
	shown := map[string]bool{}
	fromRule := func(what, id string) PolicyItem {
		shown[id] = true
		return fromRule(what, byRule[id])
	}
	audit.Policy = []PolicyItem{
		policyTitle(record),
		policyPresent("description", description, "missing; it should carry the certificate's summary"),
		policyLicence(record),
		policyIs("resource type", record.Metadata.ResourceType.ID, policyResourceType),
		policyIs("publisher", record.Metadata.Publisher, policyPublisher),
		policyPresent("language", strings.Join(ids(record.Metadata.Languages), ", "), "not set"),
		fromRule("the certificate PDF", "CC-REP-007"),
		fromRule("a machine-readable source of the certificate", "CC-REP-009"),
		policyReviews(record, configuration.Config.Paper.Reference),
		policyRepository(record),
		policyAlternate(record, "url", "http://"+certificatesURL, certificate),
		policyAlternate(record, "other", certificatesURL, certificate),
		policyCommunity(record, target.Sandbox, target.Token != ""),
	}
	if reported != nil {
		audit.Policy = append(audit.Policy, *reported)
	}
	for _, result := range report.Results {
		if !shown[result.Rule.ID] {
			audit.Rules = append(audit.Rules, result)
		}
	}
	audit.RulesSummary = Report{Results: audit.Rules}.Summary()
	return audit
}

var certificateIDIn = regexp.MustCompile(`[0-9]{4}-[0-9]{3}`)

func policyTitle(r invenioRecord) PolicyItem {
	item := PolicyItem{What: "title", Detail: r.Metadata.Title}
	switch {
	case !strings.Contains(r.Metadata.Title, "CODECHECK Certificate"):
		item.Status = StatusFail
		item.Detail = fmt.Sprintf("'%s' should read `CODECHECK Certificate <ID>`", r.Metadata.Title)
	case !certificateIDIn.MatchString(r.Metadata.Title):
		item.Status = StatusFail
		item.Detail = fmt.Sprintf("'%s' should carry the certificate identifier", r.Metadata.Title)
	default:
		item.Status = StatusPass
	}
	return item
}

// policyPresent is an item the policy asks only to be there.
func policyPresent(what, found, missing string) PolicyItem {
	if strings.TrimSpace(found) == "" {
		return PolicyItem{What: what, Status: StatusFail, Detail: missing}
	}
	return PolicyItem{What: what, Status: StatusPass, Detail: found}
}

// ids are the identifiers of a list of vocabulary entries.
func ids(entries []zenodoRight) []string {
	found := make([]string, 0, len(entries))
	for _, entry := range entries {
		found = append(found, entry.ID)
	}
	return found
}

// policyLicence asks for CC-BY 4.0 among the record's licences: the
// certificate has to be under it, and a record may carry further licences for
// the code or data deposited with it, as the R package allows.
func policyLicence(r invenioRecord) PolicyItem {
	licences := ids(r.Metadata.Rights)
	item := PolicyItem{What: "licence", Detail: strings.Join(licences, ", ")}
	switch {
	case slices.Contains(licences, ccBy4):
		item.Status = StatusPass
	case len(licences) == 0:
		item.Status, item.Detail = StatusFail, "none; the certificate should be under "+ccBy4
	default:
		item.Status = StatusFail
		item.Detail += "; the certificate should be under " + ccBy4 + ", alongside these if need be"
	}
	return item
}

func policyIs(what, value, want string) PolicyItem {
	if value == want {
		return PolicyItem{What: what, Status: StatusPass, Detail: value}
	}
	if value == "" {
		value = "none"
	}
	return PolicyItem{What: what, Status: StatusFail, Detail: fmt.Sprintf("%s, where the policy asks for `%s`", value, want)}
}

// fromRule is a policy item a register rule already decides.
func fromRule(what string, result RuleResult) PolicyItem {
	item := PolicyItem{What: what, Detail: result.Detail}
	switch result.Outcome {
	case OutcomeOK:
		item.Status = StatusPass
	case OutcomeSkipped, OutcomeUnchecked, "":
		item.Status = StatusSkip
	default:
		item.Status = StatusFail
	}
	if result.Rule.ID != "" {
		item.What += " (" + result.Rule.ID + ")"
	}
	return item
}

// policyReviews asks for a Reviews relation, and when the codecheck.yml names
// the paper by DOI, for one to that paper. The R package asks for the
// relation only.
func policyReviews(r invenioRecord, paper string) PolicyItem {
	item := PolicyItem{What: "a Reviews relation to the paper"}
	paperDOI := DOIIn(paper)
	var reviewed []string
	for _, related := range r.Metadata.RelatedIdentifiers {
		if strings.EqualFold(related.RelationType.ID, "reviews") {
			reviewed = append(reviewed, related.Identifier)
		}
	}
	switch {
	case len(reviewed) == 0:
		item.Status, item.Detail = StatusFail, "none; add the paper's DOI under Related works with the relation Reviews"
	case paperDOI == "" || slices.ContainsFunc(reviewed, func(id string) bool { return sameDOI(id, paper) }):
		item.Status, item.Detail = StatusPass, strings.Join(reviewed, ", ")
	default:
		item.Status = StatusFail
		item.Detail = fmt.Sprintf("reviews %s, where the codecheck.yml names the paper %s",
			strings.Join(reviewed, ", "), paperDOI)
	}
	return item
}

// codecheckRepository is where the policy wants a repository a certificate
// links to: the codecheckers organisation on GitHub, or cdchck on GitLab.
var codecheckRepository = regexp.MustCompile(`(?i)github\.com/codecheckers/|gitlab\.com/cdchck/`)

func policyRepository(r invenioRecord) PolicyItem {
	item := PolicyItem{What: "a relation to the repository in `codecheckers/` or `cdchck`"}
	var others []string
	for _, related := range r.Metadata.RelatedIdentifiers {
		if codecheckRepository.MatchString(related.Identifier) {
			item.Status, item.Detail = StatusPass, related.Identifier
			return item
		}
		if !strings.EqualFold(related.RelationType.ID, "reviews") {
			others = append(others, related.Identifier)
		}
	}
	item.Status, item.Detail = StatusFail, "none"
	if len(others) > 0 {
		item.Detail = strings.Join(others, ", ") + ", none of them there"
	}
	return item
}

// policyAlternate asks for the certificate's address in the register as an
// alternate identifier with a scheme. Where the certificate is known, it has
// to be that certificate's address, written as the policy writes it; the R
// package asks for the prefix only, anywhere in the identifier. Deliberate:
// the address of another certificate, or with https, is a different
// identifier, and the policy is a checklist to be followed to the letter.
func policyAlternate(r invenioRecord, scheme, prefix, certificate string) PolicyItem {
	want := prefix + certificate
	item := PolicyItem{What: fmt.Sprintf("the alternate identifier `%s<ID>` as %s", prefix, scheme)}
	var found []string
	for _, identifier := range r.Metadata.Identifiers {
		if !strings.EqualFold(identifier.Scheme, scheme) {
			continue
		}
		found = append(found, identifier.Identifier)
		if identifier.Identifier == want || (certificate == "" && strings.HasPrefix(identifier.Identifier, prefix)) {
			item.Status, item.Detail = StatusPass, identifier.Identifier
			return item
		}
	}
	item.Status = StatusFail
	switch {
	case len(found) > 0 && certificate != "":
		item.Detail = fmt.Sprintf("%s, where the certificate's is `%s`", strings.Join(found, ", "), want)
	case len(found) > 0:
		item.Detail = strings.Join(found, ", ")
	case certificate != "":
		item.Detail = fmt.Sprintf("none; add `%s` with the scheme %s", want, scheme)
	default:
		item.Detail = "none"
	}
	return item
}

// policyCommunity asks whether the record is in the community, and of a
// draft, which cannot be yet, whether it was submitted to it.
func policyCommunity(r invenioRecord, sandbox, shared bool) PolicyItem {
	community := zenodoCommunityID
	if sandbox {
		community = zenodoSandboxCommunityID
	}
	item := PolicyItem{What: "in the CODECHECK community"}
	if slices.Contains(r.Parent.Communities.IDs, community) ||
		slices.ContainsFunc(r.Parent.Communities.Entries, func(entry communityEntry) bool {
			return entry.Slug == zenodoCommunitySlug
		}) {
		item.Status, item.Detail = StatusPass, "a member"
		return item
	}
	if review := r.Parent.Review; r.IsDraft && review != nil && review.Receiver.Community == community {
		// A request that was declined, cancelled or expired will not bring
		// the record into the community.
		switch review.Status {
		case "created", "submitted", "accepted":
			item.Status, item.Detail = StatusPass, "submitted to the community, "+review.Status
		default:
			item.Status, item.Detail = StatusFail, "the request to the community was "+review.Status+"; submit it again"
		}
		return item
	}
	if r.IsDraft && shared && r.Parent.Review == nil {
		// A share link shows a draft without its request to a community,
		// which only the owner sees (Zenodo, 2026-10): no request in sight is
		// not no request. TestIntegrationSandboxDraftByShareLink notices when
		// a share link starts to show it.
		item.Status = StatusSkip
		item.Detail = "a share link does not show which community the draft was submitted to; " +
			"check that the CODECHECK community is chosen before publishing"
		return item
	}
	item.Status = StatusFail
	item.Detail = "not a member; submit it to https://zenodo.org/communities/codecheck"
	if r.IsDraft {
		item.Detail = "not submitted; choose the CODECHECK community before publishing"
	}
	return item
}

// sameRecord is an item when the codecheck.yml names a different record from
// the one read, nil when it names this one or none.
func sameRecord(report string, r invenioRecord) *PolicyItem {
	if DOIIn(report) == "" || r.PIDs.DOI.Identifier == "" {
		return nil
	}
	item := &PolicyItem{What: "the codecheck.yml's report", Status: StatusPass, Detail: "names this record"}
	if !sameDOI(report, r.PIDs.DOI.Identifier) {
		item.Status = StatusFail
		item.Detail = fmt.Sprintf("names %s, not this record's %s", DOIIn(report), r.PIDs.DOI.Identifier)
	}
	return item
}
