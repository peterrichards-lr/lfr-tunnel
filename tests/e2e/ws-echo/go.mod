// A module of its own, deliberately, and with no requirements at all (#2249).
//
// Two reasons. It keeps this fixture out of the root module's `./...`, so `make test`, `go vet`
// and golangci-lint do not acquire a test double as a package they have to reason about. And it
// makes the image build hermetic: a stdlib-only package in a dependency-free module needs no
// module download, so the E2E stack gains a service that builds in seconds off base images the
// other two services have already pulled. Importing gorilla/websocket here -- the obvious thing,
// since the root module already has it -- would have put a network fetch on the critical path of
// the job this issue exists to make cheaper.
module lfr-tunnel/tests/e2e/ws-echo

go 1.26.0
