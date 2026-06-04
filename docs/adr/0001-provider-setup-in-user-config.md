# Provider setup lives in user config

Rig stores provider setup in user-level configuration, not in the task SQLite
database. SQLite is for durable task and provider-event state, while provider
setup is user preference: configured providers and the default provider should
be easy to inspect, edit, reset, and keep stable when the task database path or
contents change.
