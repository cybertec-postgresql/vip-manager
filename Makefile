
all: vip-manager

vip-manager:
	go build .

install:
	install -d $(DESTDIR)/usr/bin
	install vip-manager $(DESTDIR)/usr/bin/vip-manager
	install -d $(DESTDIR)/lib/systemd/system
	install vip-manager.service $(DESTDIR)/lib/systemd/system/vip-manager.service
	install -d $(DESTDIR)/etc/patroni
	install vip.conf $(DESTDIR)/etc/patroni
