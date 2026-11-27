# CodeQL SAST

## Konfigurasi

Static Application Security Testing diaktifkan melalui
[`.github/workflows/codeql.yml`](.github/workflows/codeql.yml). Workflow ini
menjalankan CodeQL pada:

- Go backend: `cmd/`, `internal/`, dan package Go lain yang ter-build.
- JavaScript/TypeScript frontend: `frontend/src/`.

Workflow berjalan pada setiap push, pull request, dan setiap Senin pukul 03:23
UTC. Query suite yang digunakan adalah `security-extended`. Hasil dianalisis
oleh GitHub Code Scanning dan file SARIF disimpan sebagai artifact
`codeql-sarif-go` serta `codeql-sarif-javascript-typescript`.

CodeQL membangun database analisis terlebih dahulu, lalu menjalankan query pada
database tersebut. Karena itu, hasilnya adalah pemeriksaan statis dan bukan
pengujian request/response seperti DAST.

## Bukti hasil scan

Bukti scan tersedia pada dua lokasi setelah workflow berjalan:

1. GitHub **Security → Code scanning alerts**.
2. Artifact `codeql-sarif-*` pada run workflow **CodeQL SAST**.

Status pada repository ini saat dokumentasi dibuat: workflow sudah dikonfigurasi,
tetapi hasil scan pertama masih menunggu eksekusi GitHub Actions.

## Register temuan

| Rule | Severity | Lokasi | Potensi dampak | Keputusan | Referensi commit / bukti scan |
|---|---|---|---|---|---|
| Belum tersedia | Belum tersedia | Belum tersedia | Belum dapat dinilai sebelum scan pertama | Menunggu hasil CodeQL | Run workflow CodeQL SAST |

## Batasan

CodeQL menganalisis bahasa yang didukung dan source code yang masuk ke database
CodeQL. Dependensi pihak ketiga, konfigurasi deployment, secret runtime, dan
perilaku aplikasi saat berjalan tidak seluruhnya tercakup. Hasil bersih hanya
berarti tidak ada masalah yang ditemukan oleh query dan cakupan pada run
tersebut; hasil itu bukan jaminan aplikasi bebas kerentanan.
