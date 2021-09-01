GOENV=CGO_ENABLED=0

all: vip-manager

vip-manager: *.go */*.go
	$(GOENV) go build -ldflags="-s -w" .

install:
	install -d $(DESTDIR)/usr/bin
	install vip-manager $(DESTDIR)/usr/bin/vip-manager
	install -d $(DESTDIR)/lib/systemd/system
	install package/scripts/init-systemd.service $(DESTDIR)/lib/systemd/system/vip-manager.service
	install -d $(DESTDIR)/etc/init.d/
	install package/scripts/init-systemv.sh $(DESTDIR)/etc/init.d/vip-manager
	install -d $(DESTDIR)/etc/default
	install vipconfig/vip-manager.yml $(DESTDIR)/etc/default/vip-manager.yml

DESTDIR=tmp

.PHONY: package package/changelog.yml

package: package-deb package-rpm

package-deb: vip-manager package/changelog.yml
	nfpm package --config package/nfpm.yml --packager deb

package-rpm: vip-manager package/changelog.yml
	nfpm package --config package/nfpm.yml --packager rpm

package/changelog.yml:
	chglog init --config-file package/chglog.yml --deb-distribution unstable --deb-urgency low --output $@

clean:
	rm -f vip-manager
	rm -f vip-manager*.deb
	rm -f vip-manager*.rpm
	rm -fr $(DESTDIR)
