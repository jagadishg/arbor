# Agent safety

- Treat production and shared staging environments as protected unless the user explicitly authorizes execution.
- Never print, copy, or persist resolved secret values.
- Prefer `arbor validate` and static review before network execution.
- Treat destructive methods as requiring explicit authorization, especially outside local test environments.
- Report unresolved assumptions instead of silently guessing API behavior.
