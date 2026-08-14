# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Git

- JANGAN menambahkan atribusi Claude dalam bentuk apa pun: tanpa `Co-Authored-By: Claude`, tanpa "Generated with Claude Code" di commit message maupun PR body.

## Project

Sales Inbox — WhatsApp Web di aplikasi sendiri: admin membalas chat pelanggan memakai nomor WhatsApp perusahaan. Dua bagian terpisah:

- `backend/` — Go + whatsmeow (library WhatsApp multi-device **unofficial**), PostgreSQL (pgx; sesi whatsmeow DAN data app dalam satu database, semua tabel app ber-`account_id`), REST + WebSocket, auth session-cookie (scs + argon2id).
- `frontend/` — Next.js 16 App Router + MUI v7 (template Vuexy), halaman inbox di `/inbox`.

Seluruh teks UI dan komunikasi dengan user memakai Bahasa Indonesia.

## Commands

Backend (dari `backend/`):
```
go build ./... && go vet ./...   # verifikasi
go run .                          # jalankan (port 8080)
```
go.mod butuh Go 1.25 — toolchain ter-download otomatis via GOTOOLCHAIN=auto. Semua dependensi pure Go; JANGAN menambah dependensi yang butuh CGO (tidak ada gcc di mesin dev Windows).

Frontend (dari `frontend/`, pakai pnpm):
```
pnpm dev            # dev server port 3000
npx tsc --noEmit    # type check
pnpm lint           # eslint (import order dienforce)
pnpm build          # production build
```

CLI backend: `go run ./cmd/admin -username x -name "X" -password y [-owner]` (kelola akun admin), `go run ./cmd/migrate` (migrasi satu kali data SQLite lama → Postgres).

Env backend (dibaca dari `backend/.env` via godotenv, JANGAN commit): `POSTGRES_DSN` (wajib), `PORT`, `FRONTEND_ORIGIN` (default http://localhost:3000; menentukan CORS, origin WS, dan flag Secure cookie), `DATA_DIR` (cache file media; SENGAJA di luar folder OneDrive), `AGENT_SIGNATURE` (on/off, prefix `*Nama*:` di pesan keluar). Frontend `NEXT_PUBLIC_API_URL` (di-inline saat build; string kosong = same-origin di belakang Caddy). Postgres dev: container Docker `sales-inbox-db` port 5433.

## Arsitektur

Satu proses backend memegang SATU sesi WhatsApp (whatsmeow). File backend: `main.go` (wiring/config), `wa.go` (lifecycle WA: QR loop, event handler, ingest, kirim, media), `store.go` (app DB + query), `hub.go` (WS hub), `api.go` (HTTP handlers + CORS).

Alur data inti: event whatsmeow (`events.Message`, `events.HistorySync`, `events.Receipt`) → **satu jalur ingest bersama** → SQLite (`chats`, `messages`) → broadcast WS → reducer React di `frontend/src/views/inbox/`.

Aturan penting di jalur ingest (jangan dilanggar saat refactor):
- **Normalisasi LID**: chat `@lid` dipetakan ke JID nomor telepon (`canonicalChat`) di SETIAP titik ingest — tanpa ini chat terduplikasi antara `@lid` dan `@s.whatsapp.net`.
- **Dedup**: `INSERT ... ON CONFLICT(chat_jid,id) DO NOTHING`; broadcast WS `message` HANYA saat `RowsAffected==1` (mencegah broadcast ganda dari self-echo setelah kirim).
- History sync tidak pernah mem-broadcast frame `message` (hanya `chat_upsert` per percakapan) dan tidak menaikkan unread.
- Pesan media menyimpan raw proto (`raw_msg` BLOB) untuk di-download on-demand via `GET /api/media/{jid}/{id}` dengan cache disk di `DATA_DIR/media/`. Media lama bisa expired dari server WA (404 → "Media tidak tersedia lagi").

Kontrak API: JSON camelCase, timestamp unix milidetik. Frame WS: `status`, `qr`, `message`, `chat_upsert`, `receipt`. Tipe TypeScript padanannya di `frontend/src/types/chatTypes.ts` — perubahan kontrak harus mengubah kedua sisi.

Urutan whatsmeow yang wajib: set `store.DeviceProps` SEBELUM pairing; `GetQRChannel(ctx)` SEBELUM `Connect()`; pairing loop hanya jalan saat ada klien WS. JANGAN menjalankan dua instance backend terhadap session db yang sama (`events.StreamReplaced` = sesi saling tendang).

Frontend: state terpusat di reducer (`src/views/inbox/reducer.ts`); komponen presentasional. Layout tinggi-tetap memakai `commonLayoutClasses.contentHeightFixed` — elemen root view HARUS jadi child langsung `<main>` (page shim tidak boleh membungkus dengan div). Konvensi kode template: tanpa semicolon, single quote, import dikelompokkan dengan komentar (`// React Imports` dst.), `import type`, Tailwind logical utilities (`is-*`, `bs-*`, `min-is-0`) + `sx` MUI untuk warna theme. Ikon: `<i className='tabler-*' />`.

## Keputusan produk yang mengikat

- **Fitur blast/broadcast DITOLAK** selama memakai whatsmeow (unofficial) — pola blast memicu ban nomor WA. Jangan dibangun meski diminta stakeholder; arahkan ke WhatsApp Business API resmi.
- Session store whatsmeow (`whatsmeow.db` / tabel session) = kredensial penuh akun WA — perlakukan seperti secret.

## Status tahap 1 (2026-08-15: SELESAI diimplementasi)

Sudah jalan: repo privat `arifnurdiansyah92/sales-inbox`, PostgreSQL penuh (sesi whatsmeow + data app + tool migrasi `cmd/migrate`), auth admin (scs postgresstore + argon2id + `is_owner` + rate limit login; guard semua `/api/*` kecuali login, `/ws`, kecuali `/healthz` publik), fitur tim P0 (atribusi `admin_id`/`admin_name` + prefix nama, presence viewer via WS frame `viewing`/`presence`, `POST /api/chats/{jid}/read` global, status chat open/resolved + auto-reopen pesan masuk + `PATCH /api/chats/{jid}`, pending→failed timeout/sweep di frontend), deploy compose (Caddy same-origin `/api` `/ws` → Go, sisanya → Next **standalone**; static export TIDAK bisa karena layout template pakai `cookies()`), CI GitHub Actions, `/healthz`.

Belum (P1): TOTP 2FA, quick replies, catatan kontak, notifikasi browser, retensi media, Sentry/uptime, UI kelola admin (sementara via `cmd/admin`).
