# Engineering Principles

Core rules for every change made to this codebase. These override convenience.

## Think before coding

Before writing a line:

- State assumptions explicitly. If uncertain, ask — don't guess silently.
- If multiple interpretations exist, surface them. Don't pick one without saying so.
- If a simpler approach exists, say so and push back.
- State a brief plan for multi-step tasks and define what "done" looks like before starting.

## Simplicity first

Write the minimum code that solves the problem. Nothing speculative.

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for scenarios that cannot happen given system invariants.
- If you write 200 lines and it could be 50, rewrite it.

Ask: "Would a senior Go engineer say this is overcomplicated?" If yes, simplify.

## Surgical changes

Touch only what the task requires.

- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it — don't delete it.
- Remove only the imports/variables/functions that YOUR changes made unused.

Every changed line must trace directly to the user's request.

## Layer discipline

The call flow is: `Handler → Service → Store (DB / Redis)`. No exceptions.

- Handlers: parse request, call service, write response. Zero business logic.
- Services: business rules, orchestration, no HTTP types.
- Store: all DB and Redis access. No business logic.
- Never skip a layer (e.g., no direct DB calls from a handler).
- Never leak a layer (e.g., no `*http.Request` in a service).

## Dependency injection

Pass dependencies through constructors. Never instantiate them inline.

```go
// correct
func NewAuthHandler(authSvc *service.AuthService, auditSvc *service.AuditService) *AuthHandler
// wrong
func (h *AuthHandler) Handle(...) { svc := service.NewAuthService(db) }
```

## Observability first

Every new code path must be observable.

- Log the start and error of every significant operation using `slog` (structured, key-value pairs).
- Never swallow errors silently — log or return, never both drop.
- Audit every state-changing user action (see `api-conventions.md`).
- Health check at `/health` must reflect real dependency status.

## Test discipline

- Write tests before marking work done.
- Table-driven tests for all non-trivial logic.
- Integration tests for every new HTTP endpoint.
- Target ≥ 95% coverage on new packages; do not regress existing coverage.
- Tests must pass with `-race` flag: `make test`.

## Feature development order

For anything beyond a small bug fix:

1. Confirm requirements and success criteria before touching code.
2. Identify affected layers and write a one-paragraph plan.
3. Implement deepest layer first (Store → Service → Handler).
4. Write tests at each layer before moving up.
5. Verify the full flow with `make test` and a manual smoke test.

## No speculative infrastructure

Don't add queues, caches, workers, or abstraction layers because they "might be needed later." Add them when the need is concrete and the requirement is present.
