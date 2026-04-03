---
allowed-tools: Bash(git add:*), Bash(git status:*), Bash(git commit:*)
description: Create a git commit
---

## Commit Format
```
type(scope): description
```

## Style Rules
- Keep descriptions concise and direct
- Use present tense ("add" not "adds" or "adding")
- Lowercase after the colon
- No bullet points or lists in the body
- No "Benefits:", "Why:", or explanatory sections
- State what changed, not why it's beneficial
- Avoid redundant information
- Multi-line body only when necessary to explain what was done
- Include issue/PR numbers when relevant: `(#1234)`

## Common Types
- `feat`: new feature
- `fix`: bug fix
- `refactor`: code restructuring without functionality change
- `test`: test additions or modifications
- `docs`: documentation changes
- `chore`: maintenance tasks

## Examples

### Good
```
refactor(dbhelper): improve SQL query readability and fix trx_time
```

```
feat(utils): upgrade mattermost client to the public model (#1308)
```

```
fix(cluster): enhance error logging for SST connection listener tasks
```

```
refactor(dbhelper): split monolithic 4406-line file into 8 focused modules

Extracted functions into types.go, replication.go, status.go, performance.go,
binlog.go, transaction.go, connection.go by functional responsibility.
```

### Avoid
```
refactor(dbhelper): split monolithic 4406-line file into organized modules

Split the massive utils/dbhelper/dbhelper.go into 8 focused files organized
by functional responsibility:

- types.go (906 lines): All struct definitions and methods
- replication.go (1159 lines): Replication control operations
- status.go (430 lines): Server status and variable queries

Benefits:
- Improved code organization and maintainability
- Better testability with focused modules
```

### Constraints

Do not add claude footer
