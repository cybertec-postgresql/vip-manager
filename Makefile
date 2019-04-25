NAME=vip-manager
VERSION=0.6-1
ARCH=amd64
LICENSE="BSD 2-Clause License"
MAINTAINER="Ants Aasma <ants@cybertec.at>"
DESCRIPTION="Manages a virtual IP based on state kept in etcd/consul."
HOMEPAGE="http://www.cybertec.at/"
GIT="git://github.com/cybertec-postgresql/vip-manager.git"
GITBROWSER="https://github.com/cybertec-postgresql/vip-manager"


all: vip-manager

vip-manager: *.go */*.go
	go build -ldflags="-s -w" .

install:
	install -d $(DESTDIR)/usr/bin
	install vip-manager $(DESTDIR)/usr/bin/vip-manager
	install -d $(DESTDIR)/lib/systemd/system
	install package/scripts/init-systemd.service $(DESTDIR)/lib/systemd/system/vip-manager.service
	install -d $(DESTDIR)/etc/init.d/
	install package/scripts/init-systemv.sh $(DESTDIR)/etc/init.d/vip-manager
	install -d $(DESTDIR)/etc/default
	install package/config/vip-manager_default.yml $(DESTDIR)/etc/default/vip-manager_default.yml

DESTDIR=tmp

.PHONY: package

package: package-deb package-rpm

package-deb: vip-manager
	install -d $(DESTDIR)/usr/bin
	install vip-manager $(DESTDIR)/usr/bin/vip-manager
	install -d $(DESTDIR)/usr/share/doc/$(NAME)
	install --mode=644 package/DEBIAN/copyright $(DESTDIR)/usr/share/doc/$(NAME)/copyright
	fpm -f -s dir -t deb -n $(NAME) -v $(VERSION) -C $(DESTDIR) \
	-p $(NAME)_$(VERSION)_$(ARCH).deb \
	--license $(LICENSE) \
	--maintainer $(MAINTAINER) \
	--vendor $(MAINTAINER) \
	--description $(DESCRIPTION) \
	--url $(HOMEPAGE) \
	--deb-field 'Vcs-Git: $(GIT)' \
	--deb-field 'Vcs-Browser: $(GITBROWSER)' \
	--deb-upstream-changelog package/DEBIAN/changelog \
	--deb-no-default-config-files \
	--deb-default package/config/vip-manager_default.yml \
	--deb-systemd package/scripts/vip-manager.service \
	usr/bin usr/share/doc/

package-rpm: package-deb
	fpm -f -s deb -t rpm -n $(NAME) -v $(VERSION) -C $(DESTDIR) \
	-p $(NAME)_$(VERSION)_$(ARCH).rpm \
	$(NAME)_$(VERSION)_$(ARCH).deb

clean:
	rm -f vip-manager
	rm -f vip-manager*.deb
	rm -f vip-manager*.rpm
	rm -fr $(DESTDIR)
