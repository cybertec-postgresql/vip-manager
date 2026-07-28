package ipmanager

import (
	"net"
	"net/netip"
	"testing"
)

func TestIPConfiguration_getCIDR(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		vip     netip.Addr
		mask    net.IPMask
		want    string
		wantErr bool
	}{
		{
			name: "IPv4 /24",
			vip:  netip.MustParseAddr("192.168.1.10"),
			mask: net.CIDRMask(24, 32),
			want: "192.168.1.10/24",
		},
		{
			name: "IPv4 /32",
			vip:  netip.MustParseAddr("10.0.0.1"),
			mask: net.CIDRMask(32, 32),
			want: "10.0.0.1/32",
		},
		{
			name: "IPv4 /0",
			vip:  netip.MustParseAddr("0.0.0.0"),
			mask: net.CIDRMask(0, 32),
			want: "0.0.0.0/0",
		},
		{
			name: "IPv6 /64",
			vip:  netip.MustParseAddr("2001:db8::1"),
			mask: net.CIDRMask(64, 128),
			want: "2001:db8::1/64",
		},
		{
			name: "IPv6 /128",
			vip:  netip.MustParseAddr("::1"),
			mask: net.CIDRMask(128, 128),
			want: "::1/128",
		},
		{
			name:    "invalid mask panics",
			vip:     netip.MustParseAddr("192.168.1.10"),
			mask:    net.IPMask{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &IPConfiguration{
				VIP:     tt.vip,
				Netmask: tt.mask,
			}

			if tt.wantErr {
				defer func() {
					if r := recover(); r == nil {
						t.Errorf("expected panic, got none")
					}
				}()
			}

			got := cfg.getCIDR()
			if got != tt.want {
				t.Errorf("getCIDR() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNetmaskSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mask      net.IPMask
		want      int
		wantPanic bool
	}{
		{
			name: "IPv4 /24",
			mask: net.CIDRMask(24, 32),
			want: 24,
		},
		{
			name: "IPv4 /32",
			mask: net.CIDRMask(32, 32),
			want: 32,
		},
		{
			name: "IPv4 /0",
			mask: net.CIDRMask(0, 32),
			want: 0,
		},
		{
			name: "IPv6 /64",
			mask: net.CIDRMask(64, 128),
			want: 64,
		},
		{
			name: "IPv6 /128",
			mask: net.CIDRMask(128, 128),
			want: 128,
		},
		{
			name:      "empty mask panics",
			mask:      net.IPMask{},
			wantPanic: true,
		},
		{
			name:      "nil mask panics",
			mask:      nil,
			wantPanic: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.wantPanic {
				defer func() {
					if r := recover(); r == nil {
						t.Errorf("expected panic, got none")
					}
				}()
			}

			got := netmaskSize(tt.mask)
			if got != tt.want {
				t.Errorf("netmaskSize() = %d, want %d", got, tt.want)
			}
		})
	}
}
