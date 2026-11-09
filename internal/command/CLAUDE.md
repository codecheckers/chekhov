# internal/command

## Commands live in the registry

`internal/command/registry.go` holds one entry per command: name, aliases,
summary, usage, group, role, hidden. The parser resolves against it and the
help listing is generated from it, so a command cannot be added and then
forgotten in the documentation - which is the whole point of a registry rather
than a `switch`.

- A command someone may not run is **absent** from the listing, not shown and
  refused.
- A definition's **name may be a phrase** - `check start`, `check finish` -
  and the parser matches two words before one. A subcommand is its own entry
  with its own role, which is the whole point: `check` is for anybody and
  `check finish` is for editors, and the registry is where that is decided. A
  second word naming no entry is an argument, as it always was. `Definition.Or`
  names the other roles that may run a command, for the few where one role is
  not the whole answer, and `Who` words the refusal so it and the listing
  cannot disagree.
- A person holds a *set* of roles, `command.Roles`, not one: the same person
  can be an editor and the codechecker assigned to a check. Standing roles
  (`editor`, `codechecker`) come from the organisation's GitHub teams, read
  through `internal/people` and held in memory for a day; the per-check ones
  (`handling editor`, `assigned codechecker`, `author`) belong to one issue and
  are read from it.
  The settings file names *teams*, never people, so membership is maintained in
  one place. The cache is
  the transient copy; `Teams.Refresh` rebuilds it from the persistent one, at
  startup and whenever an editor runs `teams refresh`.
- Per-check roles, and the certificate identifier, live in a **comment the bot
  posted** - the record of the check, marked `chekhov:record` (`chekhov:roles`
  before #20, still read) - found by its marker on
  the comment's first line - not by position, and never in somebody else's
  comment. A bot comment is safe from a passer-by, **not** from a repository
  collaborator, who can edit it with the bot left as the author - so **the bot
  signs the record** (Ed25519, `internal/people/sign.go`) and refuses to change
  one it did not write. The signature covers the repository and issue too, so a
  valid record cannot be lifted between checks. An editor adopts an edited
  record with `record accept` (`accept record` and `accept roles` still work), and who adopted it
  is written into the signed block. The private key exists only in the deployment's configuration; the
  public key is published in `docs/record-keys.pub`, deliberately **not** in
  the register - the people who could edit a record must not also be able to
  change the key it is checked against. See `docs/record-key.md`. Nothing the bot posts may look like a
  record, so `act` defuses every reply once, centrally.
- **A reply to a role change is the record of it**: it names who asked and
  when, so the thread is the history and the roles comment is the current
  state. One comment, not two.
- **Roles that conflict are refused before they are granted**, in
  `internal/command/roles.go`: the assigned codechecker may not be an author of
  the paper, nor may the handling editor - CODECHECK exists so that somebody
  other than the author runs the code. The standing roles never conflict, and
  only an editor may be made the handling editor (`Role.Requires`).
- **The owners invite; the bot does the teams.** GitHub splits this endpoint by
  authority: a **team maintainer** may add somebody already in the
  organisation, and only an **organisation owner** may bring somebody in from
  outside. So `@chekhovbot` is a maintainer of the teams it manages and must
  never be an owner. `assign` asks the organisation's current owners - read
  from the organisation, and **mentioned** so they are notified - and puts the
  person in the team itself once they have joined. See #47.
- **Team membership is fenced in code.** `internal/github/team.go` is the only
  thing that changes it: one endpoint
  (`PUT /orgs/{org}/teams/{team}/memberships/{user}`), always `role: member`
  written as a literal, no role argument on any function, the configured
  organisation only, and only a team in `teams.managed` - `config.validate`
  refuses a list containing the editors team, because the bot must not be able
  to grant the permission its own editor commands are gated on. It never
  touches `PUT /orgs/{org}/memberships/{user}`, which sets the organisation
  role. Nothing there removes anybody. `team_test.go` records every request
  that leaves and fails if any of that stops being true; add to it rather than
  trusting the comments. Its errors reach a posted comment, so every handle in
  one is in backticks.
- **Fail closed.** A team the token cannot read has no members: the editor
  commands refuse and the listing does not offer them. The opposite - an
  unreadable team meaning everyone is an editor - would hand the register to
  anyone who can type. This needs *Members: read* on the token, see
  `docs/github-token.md`.
- `config/` is the single place that names the target repository, and a test
  asserts the shipped file names the testing register.
