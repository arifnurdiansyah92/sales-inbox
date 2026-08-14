# Sales Inbox

WhatsApp Web di aplikasi sendiri: tim admin membalas chat pelanggan memakai nomor WhatsApp perusahaan — dengan login admin, atribusi pengirim, presence antar admin, status percakapan, dan riwayat penuh (teks + media).

- `backend/` — Go + [whatsmeow](https://github.com/tulir/whatsmeow) + PostgreSQL (sesi WA & data chat), REST + WebSocket.
- `frontend/` — Next.js (MUI), halaman `/inbox` + `/login`.
- `deploy/` + `docker-compose.yml` — produksi VPS: Caddy (auto-TLS, same-origin) → Next standalone + backend Go; Postgres memakai yang sudah ada di infra.

## Menjalankan (development)

Prasyarat: Go 1.23+ (toolchain 1.25 otomatis), Node 22 + pnpm, PostgreSQL (dev: `docker run` container `sales-inbox-db`, lihat CLAUDE.md).

```powershell
# 1. Backend — butuh backend\.env berisi POSTGRES_DSN=postgres://...
cd backend
go run ./cmd/admin -username kamu -name "Nama" -password rahasia -owner   # buat akun (sekali)
go run .                                                                   # port 8080

# 2. Frontend
cd frontend
pnpm install
pnpm dev                                                                   # port 3000
```

Buka `http://localhost:3000/login` → masuk → `/inbox`. Pairing WhatsApp: owner membuka inbox, scan QR (WhatsApp → Perangkat Tertaut). Sesi tersimpan di Postgres.

## Fitur

- Chat realtime dua arah, riwayat penuh, media (gambar/video/audio/dokumen) kirim & terima, centang terkirim/sampai/dibaca, suara notifikasi (dering kencang / singkat / bisu).
- **Login admin** (session cookie; argon2id; rate limit; role owner vs agent). Kelola akun via `go run ./cmd/admin`.
- **Atribusi**: tiap pesan keluar tercatat admin pengirimnya ("· Rina" di bubble) + prefix `*Rina*:` di pesan WhatsApp (matikan dengan `AGENT_SIGNATURE=off`).
- **Presence**: terlihat siapa sedang membuka chat mana (anti balas-dobel).
- **Status percakapan**: Terbuka / Selesai, auto-terbuka lagi saat pelanggan membalas.
- **Unread global tim** yang persisten (server-side mark-read).
- `/healthz` untuk monitoring.

## Deploy (VPS)

1. Salin `.env.example` → `.env` (isi `POSTGRES_DSN` ke Postgres infra, `SITE_ADDRESS` domain, `FRONTEND_ORIGIN` https://domain), `chmod 600 .env`.
2. `docker compose up -d --build` — Caddy mengurus TLS; `/api` & `/ws` ke backend, sisanya ke frontend. Same-origin: tanpa CORS.
3. Migrasi data dari instalasi SQLite lama (opsional, sekali): `go run ./cmd/migrate -sqlite-dir <folder-data-lama>`.
4. Buat admin: `docker compose exec backend /app/migrate --help` — atau jalankan `cmd/admin` dari mesin yang bisa akses Postgres.

## Keputusan & batasan penting

- **Jangan bangun fitur blast/broadcast** selama memakai whatsmeow (unofficial) — pola blast memicu banned nomor. Balas chat masuk = risiko rendah.
- Tabel `whatsmeow_*` di Postgres = kredensial penuh akun WA. Batasi akses DB, enkripsi backup.
- Satu instance backend per sesi WA. Dua instance = saling tendang (StreamReplaced).
- Media lama bisa kedaluwarsa dari server WhatsApp ("Media tidak tersedia lagi") — normal.
