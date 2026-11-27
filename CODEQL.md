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

Status scan lokal awal pada 27 November 2026:  
Hasil awalnya adalah 7 alert pada Go dan 0 alert pada JavaScript/TypeScript. File SARIF awal yang menjadi bukti
adalah:

- [`codeQLresults/go.sarif`](codeQLresults/go.sarif)
- [`codeQLresults/javascript.sarif`](codeQLresults/javascript.sarif)

Scan Go awal menganalisis 9.480 lines of code. Setelah perbaikan integer
conversion, scan ulang menganalisis 9.498 lines of code dan menghasilkan 6
alert Go. Bukti scan ulang adalah
[`codeQLresults/go-rescan.sarif`](codeQLresults/go-rescan.sarif). Scan
JavaScript/TypeScript tetap menghasilkan 0 alert.

## Register temuan

| Rule | Severity | Lokasi | Potensi dampak | Keputusan | Referensi commit / bukti scan |
|---|---|---|---|---|---|
| `go/incorrect-integer-conversion` | High (security-severity 8.1) | `internal/migration/lock.go:93` | ID database bertipe `int64` dipotong menjadi `int32`. ID besar dapat berubah nilai dan menyebabkan advisory-lock collision atau penggunaan lock yang salah. | Temuan valid dan diperbaiki dengan validasi batas `int32` sebelum konversi. Scan ulang tidak lagi melaporkan rule ini. | [`go.sarif`](codeQLresults/go.sarif), result 1. [`go-rescan.sarif`](codeQLresults/go-rescan.sarif) |
| `go/cookie-secure-not-set` | Medium (4.0) | `internal/session/cookie.go:38` | Cookie sesi dapat dikirim melalui HTTP jika konfigurasi `Secure` dimatikan. | Risiko diterima hanya untuk development HTTP lokal. Default konfigurasi production tetap `true`. Deployment production wajib mempertahankan `SESSION_SECURE_COOKIES=true`. | [`go.sarif`](codeQLresults/go.sarif), result 2 |
| `go/cookie-secure-not-set` | Medium (4.0) | `internal/session/cookie.go:53` | Cookie penghapusan sesi memiliki risiko konfigurasi yang sama jika `Secure` dimatikan. | Risiko diterima untuk development HTTP lokal dengan alasan yang sama. Temuan ini bukan false positive. | [`go.sarif`](codeQLresults/go.sarif), result 3 |
| `go/log-injection` | Medium (6.1) | `internal/admin/api.go:65` | Nilai error yang dipengaruhi input dapat masuk ke log dan berpotensi memalsukan baris log jika logger tidak melakukan escaping. | False positive dalam deployment ini. Output diarahkan ke `slog.JSONHandler`, yang melakukan JSON escaping terhadap string dan karakter kontrol. Logger plaintext tetap harus dihindari untuk production. | [`go.sarif`](codeQLresults/go.sarif), result 4 |
| `go/log-injection` | Medium (6.1) | `internal/migration/handler.go:429` | Nilai error dari alur migration dapat masuk ke log. | False positive dengan alasan yang sama. Logger production menggunakan JSON encoding sehingga input tidak dapat membuat record JSON baru. | [`go.sarif`](codeQLresults/go.sarif), result 5 |
| `go/log-injection` | Medium (6.1) | `internal/observability/observability.go:114` | Atribut trace/log yang berasal dari request dapat mengandung karakter kontrol. | False positive untuk sink JSON terstruktur karena `slog` melakukan escaping. Logging tetap dibatasi pada field observability dan tidak memasukkan secret. | [`go.sarif`](codeQLresults/go.sarif), result 6 |
| `go/log-injection` | Medium (6.1) | `internal/observability/observability.go:114` | Temuan kedua pada pemanggilan sink yang sama untuk atribut lain. Risikonya sama. | False positive dengan alasan yang sama. Hasil tetap dipertahankan sebagai dua result SARIF terpisah. | [`go.sarif`](codeQLresults/go.sarif), result 7 |

### Frontend

`javascript.sarif` berisi 0 result dari suite `security-extended`. Cakupan yang
dianalisis adalah source TypeScript/React di `frontend/src/`. Hasil ini tidak
membuktikan frontend aman sepenuhnya; dependensi, konfigurasi browser, dan
perilaku runtime tetap memerlukan pemeriksaan lain.

### Tindak lanjut

Perbaikan integer conversion sudah diverifikasi melalui scan ulang: alert
berkurang dari 7 menjadi 6. Perubahan kode dan test batasnya berada pada
working tree dan harus dicatat dalam commit saat perubahan ini di-push.
Temuan cookie dan log injection tetap didokumentasikan sebagai risiko
konfigurasi development dan false positive sink JSON terstruktur.

## Batasan

CodeQL menganalisis bahasa yang didukung dan source code yang masuk ke database
CodeQL. Dependensi pihak ketiga, konfigurasi deployment, secret runtime, dan
perilaku aplikasi saat berjalan tidak seluruhnya tercakup. Hasil bersih hanya
berarti tidak ada masalah yang ditemukan oleh query dan cakupan pada run
tersebut; hasil itu bukan jaminan aplikasi bebas kerentanan.
