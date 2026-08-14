# Sales Inbox — WhatsApp Web di Aplikasi Sendiri

Admin bisa membalas chat memakai akun WhatsApp kamu, langsung dari aplikasi ini.

- `backend/` — Go + [whatsmeow](https://github.com/tulir/whatsmeow): koneksi WhatsApp (multi-device), simpan sesi & riwayat chat di SQLite, expose REST + WebSocket.
- `frontend/` — Next.js (template Vuexy MUI): halaman **Inbox** (`/inbox`) bergaya WhatsApp Web.

## Cara Menjalankan

### 1. Backend (port 8080)

```powershell
cd backend
go run .
```

Build pertama akan otomatis mengunduh toolchain Go 1.25 (whatsmeow membutuhkannya) — tunggu saja.

Environment variable (opsional):

| Variabel | Default | Keterangan |
|---|---|---|
| `PORT` | `8080` | Port HTTP backend |
| `FRONTEND_ORIGIN` | `http://localhost:3000` | Origin frontend untuk CORS + WebSocket |
| `DATA_DIR` | `%LOCALAPPDATA%\sales-inbox` | Lokasi file SQLite (sesi WA + riwayat chat). Sengaja di luar folder OneDrive — jangan pindahkan ke folder yang disinkronkan OneDrive, bisa merusak database. |

### 2. Frontend (port 3000)

```powershell
cd frontend
pnpm install   # sekali saja
pnpm dev
```

Buka `http://localhost:3000/inbox`.

### 3. Hubungkan WhatsApp

1. Halaman Inbox akan menampilkan kode QR.
2. Di HP: WhatsApp → Menu ⋮ → **Perangkat Tertaut** → **Tautkan Perangkat** → scan QR.
3. Setelah tersambung, riwayat chat masuk otomatis (history sync dari HP, hanya dikirim beberapa saat setelah pairing). Chat baru muncul realtime; balas langsung dari kolom pesan.

Sesi tersimpan di SQLite — restart backend tidak perlu scan ulang. Tombol colokan (kanan atas sidebar) = putuskan koneksi / logout (perlu scan ulang).

## Arsitektur Singkat

```
Next.js (/inbox) ──REST──▶ Go backend ──whatsmeow──▶ WhatsApp
        ▲                      │
        └──── WebSocket ◀──────┘  (status, kode QR, pesan masuk, update chat)
```

- REST: `GET /api/status`, `GET /api/chats`, `GET|POST /api/chats/{jid}/messages`, `POST /api/logout`, `GET /api/avatar/{jid}`
- WebSocket `/ws`: event `status`, `qr`, `message`, `chat_upsert`
- Pesan media (gambar/video/dokumen/audio/stiker) tampil sebagai placeholder `[Gambar]` dst. (MVP — belum download media)

## Batasan MVP

- Tanpa login admin (siapa pun yang bisa akses frontend bisa membalas chat) — jangan diekspos ke internet dulu.
- Satu akun WhatsApp.
- Kirim/terima teks saja; media tampil sebagai placeholder.
- Badge unread di-reset saat chat dibuka; setelah refresh halaman, chat yang sedang terbuka bisa menampilkan badge lagi sampai diklik (belum ada endpoint read-receipt).
