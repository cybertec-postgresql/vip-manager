GOENV=CGO_ENABLED=0

all: vip-manager

vip-manager: *.go */*.go
	$(GOENV) go build -ldflags="-s -w" .

install:
	install -d $(DESTDIR)/usr/bin
	install vip-manager $(DESTDIR)/usr/bin/vip-manager
	install -d $(DESTDIR)/etc/default
	install vipconfig/vip-manager.yml $(DESTDIR)/etc/default/vip-manager.yml

DESTDIR=tmp

package: 
	goreleaser release --snapshot --skip-publish --rm-dist

clean:
	$(RM) vip-manager
	$(RM) -r dist
	$(RM) -r $(DESTDIR)