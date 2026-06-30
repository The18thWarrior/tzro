# Spec Writing Guide

The local model is a 4B parameter model. Help it succeed with well-crafted specs.

## DO

- Include exact function signatures it should produce
- Reference specific line numbers or function names for updates
- Specify the package name and imports needed
- Keep specs under 200 words — concise and precise

## DON'T

- Leave architectural decisions to the local model
- Ask it to "refactor" without specifying the target shape
- Assume it knows your project's conventions (state them)
- Give it vague specs like "add proper error handling"

## Examples

**Good spec:**
```
Add a Paginate method to UserRepository (internal/repository/user.go).
Signature: func (r *UserRepository) Paginate(ctx context.Context, limit, offset int) ([]User, error)
Use r.db.QueryContext with "SELECT * FROM users ORDER BY id LIMIT ? OFFSET ?".
Scan into User structs. Return (nil, err) on query or scan error.
```

**Bad spec:**
```
Add pagination support to the user repository.
```

## Convergence Rules

- **Max 2 fix-up cycles.** If issues persist after 2 rounds, present what you have and flag the remaining issues for the user.
- **Fix-ups should be surgical.** Each fix-up task targets one specific issue in one file. Don't re-generate entire files for a one-line fix.
- **Build must pass before sign-off.** Never present results without running `go build` (or equivalent).
