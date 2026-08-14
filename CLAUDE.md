# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Git

- JANGAN menambahkan atribusi Claude dalam bentuk apa pun: tanpa `Co-Authored-By: Claude`, tanpa "Generated with Claude Code" di commit message maupun PR body.

## Project

Sales Inbox — WhatsApp Web di aplikasi sendiri: admin membalas chat pelanggan memakai nomor WhatsApp perusahaan. Dua bagian terpisah:

- `backend/` — Go + whatsmeow (library WhatsApp multi-device **unofficial**), SQLite (modernc, pure Go), REST + WebSocket.
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

Env: backend `PORT`, `FRONTEND_ORIGIN` (default http://localhost:3000), `DATA_DIR` (default `%LOCALAPPDATA%\sales-inbox` — SENGAJA di luar folder OneDrive; sync OneDrive merusak SQLite WAL). Frontend `NEXT_PUBLIC_API_URL` (di-inline saat build).

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

## Roadmap tahap 1 (disepakati)

Deploy VPS, 1 nomor WA, 2–5 admin, internal dulu (SaaS menyusul). Urutan: git repo → migrasi ke **PostgreSQL** (sudah tersedia di infra user; session store whatsmeow via `sqlstore.NewWithDB` + driver pgx, app DB di-port dari SQLite, tambah kolom `account_id` hardcode 1) → auth admin (session cookie `alexedwards/scs` + argon2id, flag `is_owner`) → Caddy same-origin (`/api` + `/ws` ke Go, sisanya Next static export) → fitur tim P0: atribusi pengirim (`admin_id`), presence "sedang membuka chat", mark-read persisten global, status Open/Selesai, tandai pesan gagal + retry.
