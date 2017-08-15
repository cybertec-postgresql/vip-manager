
all: vip-manager

vip-manager: main.go ip_manager.go ip_configuration.go checker/leader_checker.go checker/consul_leader_checker.go checker/etcd_leader_checker.go
	go build .

install:
	install -d $(DESTDIR)/usr/bin
	install vip-manager $(DESTDIR)/usr/bin/vip-manager
	install -d $(DESTDIR)/lib/systemd/system
	install vip-manager.service $(DESTDIR)/lib/systemd/system/vip-manager.service
	install -d $(DESTDIR)/etc/patroni
	install vip.conf $(DESTDIR)/etc/patroni
