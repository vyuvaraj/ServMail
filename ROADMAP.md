# ServMail Roadmap

This roadmap outlines the planned development phases for the ServMail notification provider service.

---

## Phase 1: Core Mail & Notifications (In Progress)
- [x] **Multi-channel delivery** — SMTP email, Slack webhooks, and SMS. [June 29, 2026]
- [x] **Template rendering engine** — Go-template parser with context injection. [June 29, 2026]
- [x] **Opt-out preferences** — Global subscription management. [June 29, 2026]
- [x] **Serv-lang integration** — `mail.send()` and `notify()` builtins. [June 29, 2026]

## Phase 2: Delivery Control
- [x] **Dead letter queue retry** — Retry policies on delivery failure. [June 29, 2026]
- [x] **Email tracking** — Open/click analytics. [June 29, 2026]
- [x] **Attachments cold tier** — Eviction of large attachment payloads. [June 29, 2026]
- [x] **Rate limiting** — Per-recipient throttling to prevent spam. [June 29, 2026]
- [x] **Template versioning** — Versioned template support. [June 29, 2026]
- [x] **Delivery dashboard** — Integrated telemetry metrics logs. [June 29, 2026]

## Phase 3: Production Hardening & Resilience (Completed)
- [x] **State-Isolated Unit & Validation Tests** — Table-driven checks for missing recipients and unsupported channels. [June 30, 2026]
- [x] **Interface Abstractions & Decoupled Storage** — Extract templates repository behind `TemplateStore` interface for mockability. [June 30, 2026]
- [x] **Structured Logging & OTel Tracing** — Add TraceMiddleware for tracing context propagation and JSON log format. [June 30, 2026]
- [x] **SIGTERM Graceful Shutdown** — Register listener to shut down HTTP listener cleanly with a 5-second timeout. [June 30, 2026]

## Phase 4: Developer Experience (Pending)
- [ ] **Local Mock Dev Server** — Offline SMTP mock responses for local testing (DX.9)

## Phase 5: Package Extraction & Advanced Delivery (Pending — July 2026)

> **Issue:** `main.go` is 673 lines + 42-line store.go. No `pkg/` structure.

| # | Item | Effort | Description | Status |
|---|------|--------|-------------|--------|
| 5.1 | **Extract `pkg/delivery/`** | Medium | Move channel-specific delivery logic (SMTP, Slack, Webhook, SMS) behind a `DeliveryChannel` interface | [ ] |
| 5.2 | **Extract `pkg/templates/`** | Small | Move template rendering, versioning, and variable injection into dedicated package | [ ] |
| 5.3 | **Extract `pkg/tracking/`** | Small | Move open/click/bounce tracking into dedicated package with proper analytics pipeline | [ ] |
| 5.4 | **Delivery receipt webhooks** | Medium | POST delivery status (delivered/bounced/opened/clicked) to configurable callback URLs for external systems | [ ] |
| 5.5 | **Template inheritance** | Small | Support base templates with blocks that child templates override (like Django template inheritance) | [ ] |
| 5.6 | **Bulk send API** | Medium | Send to multiple recipients in one API call with per-recipient variable substitution. Queue internally via ServQueue | [ ] |
| 5.7 | **Scheduled sends** | Small | Accept `send_at` parameter to delay delivery. Integrate with ServCron for precise timing | [ ] |

> See [UNIFIED_ROADMAP.md](../servverse-repo/UNIFIED_ROADMAP.md) for the full ecosystem priority matrix.
