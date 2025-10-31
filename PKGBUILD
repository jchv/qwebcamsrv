pkgname=qwebcamsrv
pkgver=1.0.0
pkgrel=1
pkgdesc="Simple webcam server primarily meant for Triforce games"
arch=('x86_64')
url="https://github.com/jchv/qwebcamsrv"
license=('ISC')
depends=()
makedepends=('go')
source=("git+file://$PWD")
sha256sums=('SKIP')

build() {
  cd "$srcdir/$pkgname"
  export CGO_CPPFLAGS="${CPPFLAGS}"
  export CGO_CFLAGS="${CFLAGS}"
  export CGO_CXXFLAGS="${CXXFLAGS}"
  export CGO_LDFLAGS="${LDFLAGS}"
  export GOFLAGS="-buildmode=pie -trimpath -ldflags=-linkmode=external -mod=readonly -modcacherw"
  go build -o $pkgname .
}

package() {
  cd "$srcdir/$pkgname"
  install -Dm755 "$pkgname" "$pkgdir/usr/bin/$pkgname"
  install -Dm644 LICENSE "$pkgdir/usr/share/licenses/$pkgname/LICENSE" 2>/dev/null || true
  install -Dm644 "qwebcamsrv@.service" "$pkgdir/usr/lib/systemd/system/qwebcamsrv@.service"
  install -Dm644 "qwebcamsrv.sysusers" "$pkgdir/usr/lib/sysusers.d/qwebcamsrv.conf"
}
