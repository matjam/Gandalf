# Python

- Type hints everywhere: functions, class attributes, module-level values.
- Use `uv` for dependency management, environments, and running scripts.
- Use `ruff` for both linting and formatting, configured in `pyproject.toml`.
- Prefer `pathlib` over `os.path`, and an async-capable HTTP client over the
  synchronous one you already know.
- Raise specific exceptions and catch specific exceptions. A bare `except` is a bug
  waiting for a maintainer.

## Services

- Keep domain logic pure: no framework imports, no infrastructure, no I/O. It should
  run without a web server present.
- Validate at the boundary with a schema library rather than by hand.
- Inject dependencies at the adapter boundary. Do not reach for module-level
  singletons because they are convenient.
- Use structured logging. No bare `print`, no f-strings interpolated into log calls —
  pass fields, not sentences.
- Load and validate configuration at startup, and fail loudly if it is wrong. Give each
  component what it needs rather than a handle to the whole config.

## Scripts

- Keep them short, linear, and commented. Ports and adapters is overkill here.
- Past roughly 200 lines, or once a script has two concerns, it is a module. Move it.
- Type hints and explicit error handling still apply.
