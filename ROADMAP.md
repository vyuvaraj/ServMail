# ServMail Roadmap

This roadmap outlines the planned development phases for the ServMail notification provider service.

---

## Phase 1: Core Mail & Notifications (In Progress)
- [x] **Multi-channel delivery** — SMTP email, Slack webhooks, and SMS. [June 29, 2026]
- [x] **Template rendering engine** — Go-template parser with context injection. [June 29, 2026]
- [ ] **Opt-out preferences** — Global subscription management.
- [x] **Serv-lang integration** — `mail.send()` and `notify()` builtins. [June 29, 2026]

## Phase 2: Delivery Control
- [x] **Dead letter queue retry** — Retry policies on delivery failure. [June 29, 2026]
- [ ] **Email tracking** — Open/click analytics.
- [ ] **Attachments cold tier** — Eviction of large attachment payloads.
- [x] **Rate limiting** — Per-recipient throttling to prevent spam. [June 29, 2026]
- [x] **Template versioning** — Versioned template support. [June 29, 2026]
