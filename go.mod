module github.com/timesking/oauth2_proxy

require (
	cloud.google.com/go v0.34.0 // indirect
	github.com/BurntSushi/toml v0.3.0
	github.com/bitly/go-simplejson v0.5.0
	github.com/bitly/oauth2_proxy v2.0.1+incompatible
	github.com/coreos/go-oidc v0.0.0-20171026214628-77e7f2010a46
	github.com/mbland/hmacauth v0.0.0-20170912224942-107c17adcc5e
	github.com/mreiferson/go-options v0.0.0-20161229190002-77551d20752b
	github.com/pquerna/cachecontrol v0.0.0-20180517163645-1555304b9b35 // indirect
	github.com/stretchr/testify v1.1.4
	golang.org/x/crypto v0.0.0-20171113213409-9f005a07e0d3
	golang.org/x/net v0.0.0-20181220203305-927f97764cc3 // indirect
	golang.org/x/oauth2 v0.0.0-20171106152852-9ff8ebcc8e24
	google.golang.org/api v0.0.0-20171116170945-8791354e7ab1
	gopkg.in/fsnotify.v1 v1.2.11
	gopkg.in/square/go-jose.v2 v2.2.1 // indirect
)

replace github.com/bitly/oauth2_proxy v2.0.1+incompatible => ./
