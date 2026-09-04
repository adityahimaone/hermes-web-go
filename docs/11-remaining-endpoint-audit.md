# Audit klasifikasi 41 sisa endpoint Python WebUI → Go (2026-09-04)

Legenda:
- [NATIVE-ABLE] = bisa di-port ke Go tanpa runtime Python (file ops, git, static reads)
- [AGENT] = butuh runtime agent (SESSION_AGENT_CACHE, streaming agent, LLM call)
- [PLATFORM] = kontrol proses/platform Python (reload modul, gateway restart)
- [GATE-HEAVY] = butuh auth/webauthn/crypto platform yang belum ada di Go
- [MISC] = kecil, bisa, tapi low-value / butuh konvensi UI spesifik

## File & folder ops (workspace-anchored) — NATIVE-ABLE, prioritas tertinggi
1. /api/file/rename — safe_resolve + symlink lstat guard + rename_anchored. Go sudah punya workspaceRootForSession + workspace.ErrOutsideRoot pattern (data.go file/save|delete). Port: ~80 baris.
2. /api/file/move — openat/dir_fd race-safe di Python. Go: os.Rename + symlink checks + collision check (tanpa dir_fd; resiko TOCTOU jauh lebih rendah di Go karena kita bisa pakai os.OpenRoot (Go 1.24+) — parity aman).
3. /api/file/create-dir — make_anchored_dir. Port trivial.
4. /api/file/path — resolve path → absolute (tanpa require exists). Trivial.
5. /api/file/reveal — subprocess `open -R` / `explorer.exe /select` / xdg-open. Trivial.
6. /api/file/open-vscode — subprocess `code` + path translation config. Trivial.
7. /api/file/office-save — save + cache-bust; mirip file/save. ~40 baris.
8. /api/folder/download — zip stream dari workspace subtree, skip symlink escape, 413 pre-flight. Go archive/zip. ~120 baris.

## Shares — NATIVE-ABLE
9. /api/share/create — snapshot pesan + token_urlsafe(18) + shares/<token>.json + simpan share_token di session JSON. Go sudah serve /share/{id} shell. Butuh redaction parity (_force_redact_credentials) — port subset redaksi. ~100 baris.
10. /api/share/revoke — revoke_share + clear session fields. ~40 baris.

## Skills read API — NATIVE-ABLE (list/content/usage sudah partially native)
11. /api/skills — SUDAH native (chi) tapi belum ada di NativeMethods map. Verifikasi shape vs Python (direktori aktif, metadata), lalu daftarkan di registry.
12. /api/skills/content — SUDAH native chi. Verifikasi parity + registry.
13. /api/skills/usage — SUDAH native chi. Idem.

## Media/serve — NATIVE-ABLE (dengan catatan)
14. /api/media — serve file by absolute path dengan allowed-roots allowlist + auth gate + SVG attachment guard. Go: port roots allowlist + content-type. ~100 baris.

## Chat/agent runtime — AGENT (butuh SESSION_AGENT_CACHE / streaming runner Python)
15. /api/btw — hidden session + SSE stream via agent runtime.
16. /api/background — daemon-thread agent run.
17. /api/background/status — status background agent runs.
18. /api/goal — goal command → agent kickoff stream.
19. /api/chat/steer — inject steer ke agent loop berjalan.
20. /api/clarify/respond — respond ke clarify request agent runtime.
21. /api/approval/stream — SSE stream approval events dari agent runtime.
22. /api/clarify/stream — idem clarify.

## LLM-coupled
23. /api/git/commit-message — LLM generate.
24. /api/git/commit-message-selected — LLM generate.
25. /api/session/compress — LLM compression job.
26. /api/session/compression-recovery/start — butuh session fork + agent runtime state (focused continuation) → sebagian native-able tapi butuh agent context. AGENT untuk stream-nya.
27. /api/updates/summary — LLM summarize via profile env worker.

## Platform Python-side
28. /api/health/restart — restart gateway service via Python supervisor (restart_active_profile_gateway). Go tidak mengelola gateway Python.
29. /api/commands/exec — execute agent-side runtime commands (reload-mcp, reload-skills → Python import tables).
30. /api/commands/moa/resolve — resolve MoA (mixture-of-agents) config via agent.
31. /api/commands/bundles/resolve — bundle command resolve dari agent packages. (Bisa native kalau bundle manifests dibaca dari disk — cek api/commands.py; kemungkinan [MISC].)

## Auth platform — GATE-HEAVY (butuh webauthn/oidc libs + session infra)
32. /api/auth/oidc/start
33. /api/auth/oidc/callback
34. /api/auth/passkey/options
35. /api/auth/passkey/register
36. /api/auth/passkey/register/options
37. /api/auth/passkey/login
38. /api/auth/passkey/delete
39. /api/auth/passkeys

## Test-only
40. /api/approval/inject_test — loopback-only test hook (approval store Go sudah ada; bisa native trivial tapi zero prod value).
41. /api/clarify/inject_test — idem.

## Kandidat wave berikutnya (native-able, non-agent):
- Wave 19: file ops family (1-8) — 8 endpoint, semua pattern sudah ada di Go.
- Wave 20: share create/revoke (9-10) + media (14) — 3 endpoint.
- Wave 21 (opsional): registry hygiene — daftarkan 8 chi-native yang belum ada di NativeMethods (chat family, skills, memory) + verifikasi parity skills.
Setelah itu sisa 30 endpoint semuanya AGENT/GATE-HEAVY/PLATFORM.
