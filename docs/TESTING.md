# Testing Strategy & Documentation (`TESTING.md`)

Dokumen ini menjelaskan arsitektur pengujian otomatis (automated testing), pemisahan antara **unit test** dan **integration test**, cakupan skenario pengujian (sukses, gagal/rollback, penolakan akses, konkurensi), serta cara menjalankan seluruh test secara lokal maupun di CI.

---

## 1. Arsitektur Pengujian

Pengujian dibagi menjadi dua lapisan utama:

1. **Unit Tests (`go test -race ./...`)**
   - Berjalan **tanpa** ketergantungan eksternal (tidak membutuhkan koneksi PostgreSQL aktif).
   - Menggunakan *in-memory stub*, *fake clock*, dan *mock interface* (`Repository`, `Locker`, `Executor`, `AccessChecker`).
   - Memverifikasi logika bisnis murni:
     - Hashing & verifikasi password (`internal/auth`)
     - Validasi konfigurasi environment (`internal/config`)
     - SQL Validator: penolakan SQL kosong, komentar saja, statement non-transaksional (`CREATE INDEX CONCURRENTLY`, `DROP INDEX CONCURRENTLY`, `REINDEX CONCURRENTLY`, `VACUUM`, `CLUSTER`, `CREATE/DROP DATABASE`, `ALTER SYSTEM`), dan kontrol transaksi manual (`BEGIN`, `COMMIT`, `ROLLBACK`)
     - State machine status migration (`draft` → `applied` / `failed`; pencegahan double-execute & perubahan setelah bukan `draft`)
     - Penentuan versi sekuensial (`max(version) + 1` per database)
     - Otorisasi berlapis di backend (`Service` dan HTTP `Handler` / middleware `RequireDBAccess`): membedakan pengguna tanpa grant (`LevelNone`), `View Only` (`LevelView`), dan `View + Edit` (`LevelEdit`)

2. **Integration Tests (`go test -race -tags=integration ./...`)**
   - Menggunakan build tag `//go:build integration` dan berjalan terhadap instance **PostgreSQL 16** nyata yang didefinisikan pada `docker-compose.test.yml` (port `5433`, penyimpanan `tmpfs` agar cepat dan terisolasi).
   - Menggunakan package helper `internal/testutil` untuk:
     - Mereset schema `public` dan menjalankan seluruh migration internal platform (`migrations/platform/*.sql`).
     - Membuat schema terisolasi (`OpenIsolatedSchemaPool`) dengan `search_path` terpisah untuk menyimulasikan database target (`managed_databases`) secara independen dari tabel internal platform.
     - Menyediakan fungsi *fixture seeding* (`SeedUser`, `SeedGroup`, `SeedUserGroup`, `SeedManagedDatabase`, `SeedGroupGrant`).

---

## 2. Skenario Pengujian Inti yang Dicakup

| Kategori | Skenario | Hasil yang Diverifikasi |
|---|---|---|
| **Alur Sukses** | Buat draft migration → Preview SQL → Execute (dengan konfirmasi) | Perubahan DDL/DML terlihat di DB target; status di DB platform berubah menjadi `applied` dengan `version = 1`, `executed_by`, dan `executed_at` terisi. |
| **Alur Gagal & Rollback** | Multi-statement SQL gagal di tengah eksekusi (mis. statement ke-1 `CREATE TABLE` + `INSERT`, statement ke-3 gagal karena tabel/sintaks tidak valid) | Transaksi pada DB target di-**rollback total** (tabel dari statement ke-1 tidak terbentuk); riwayat di DB platform **tetap tercatat** dengan status `failed`, nomor `version` linear berikutnya, serta detail `error_message`. |
| **Validasi Non-Transaksional** | Input SQL berisi `CREATE INDEX CONCURRENTLY`, `VACUUM`, `DROP DATABASE`, `ALTER SYSTEM`, atau SQL kosong/komentar | Ditolak sebelum menyentuh DB target dengan error `ErrNonTransactionalSQL` / `ErrEmptySQL` (`400 Bad Request`). |
| **Penolakan Akses (`LevelNone`)** | User tanpa group grant (termasuk Platform Admin yang tidak tergabung di group DB tersebut) mengakses list/detail/create/execute | Ditolak di backend dengan `ErrForbidden` (`403 Forbidden`). Membuktikan bahwa `is_platform_admin` tidak otomatis memberi akses ke DB target tanpa group grant. |
| **Penolakan Akses (`LevelView`)** | User dengan akses group `View Only` mencoba membuat draft, mengedit draft, preview, atau mengeksekusi migration | Melihat riwayat & detail diizinkan (`200 OK`, tombol create/execute disembunyikan di UI); percobaan akses langsung ke endpoint `GET /new`, `POST /migrations`, atau `POST /execute` ditolak di backend dengan `403 Forbidden`. |
| **Multi-Group Union Rule** | User tergabung di group `view` sekaligus group `edit` pada DB yang sama | Akses efektif mengikuti aturan **union / tertinggi menang** (`LevelEdit`), sehingga diizinkan membuat dan mengeksekusi migration. |
| **Konkurensi & Linearitas Versi** | Eksekusi paralel beberapa draft migration terhadap DB target yang sama secara bersamaan | `pg_advisory_lock` menserialisasi eksekusi per database sehingga versi tetap berurutan (`1, 2, 3, ...`) tanpa duplikat/konflik; percobaan eksekusi paralel pada draft yang sama hanya mengeksekusi tepat satu kali (`ErrAlreadyApplied` untuk sisanya). |

---

## 3. Cara Menjalankan Pengujian

### A. Menjalankan Unit Tests (Tanpa Docker)

```bash
make test
# atau langsung:
go test -race ./...
```

### B. Menjalankan Integration Tests (Dengan Docker Compose)

Pastikan Docker Desktop / Docker Engine aktif, lalu jalankan:

```bash
# 1. Nyalakan container PostgreSQL khusus test (port 5433)
docker compose -f docker-compose.test.yml up -d --wait

# 2. Jalankan seluruh unit + integration test
TEST_DATABASE_URL="postgres://test:test@localhost:5433/platform_test?sslmode=disable" go test -race -tags=integration ./...

# 3. Matikan container test setelah selesai
docker compose -f docker-compose.test.yml down -v
```

Atau menggunakan `Makefile` (Linux/macOS/WSL/Git Bash):

```bash
make test-integration
make test-db-down
```

---

## 4. Continuous Integration (GitHub Actions)

File `.github/workflows/ci.yml` menjalankan dua *job* secara otomatis pada setiap `push` ke `main` maupun `pull_request`:
1. **`unit`**: Menjalankan `go vet ./...` dan `go test -race ./...`.
2. **`integration`**: Menyiapkan service container `postgres:16` dengan *healthcheck* otomatis dan menjalankan `go test -race -tags=integration ./...`.
