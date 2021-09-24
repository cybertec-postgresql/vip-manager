package ipmanager

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"

	"github.com/hetznercloud/hcloud-go/hcloud"
)

type HetznerCloudConfigurer struct {
	config       *vipconfig.Config
	client       *hcloud.Client
	server       *hcloud.Server
	floatingIP   *hcloud.FloatingIP
	cachedState  int
	lastAPICheck time.Time
	verbose      bool
}

func newHetznerCloudConfigurer(config *vipconfig.Config) (*HetznerCloudConfigurer, error) {
	c := &HetznerCloudConfigurer{
		config:          config,
		cachedState:     unknown,
		lastAPICheck:    time.Unix(0, 0),
		verbose:         config.Verbose,
	}

	if err := c.createHetznerClient(); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *HetznerCloudConfigurer) queryAddress() bool {
	if (time.Since(c.lastAPICheck) / time.Hour) > 1 {
		/**We need to recheck the status!
		 * Don't check too often because of stupid API rate limits
		 */
		if c.verbose {
			log.Println("Cached state was too old.")
		}

		c.cachedState = unknown
	} else {
		/** no need to check, we can use "cached" state if set.
		 * if it is set to UNKNOWN, a check will be done.
		 */
		if c.verbose {
			log.Printf("queryAddress: returning cached status %+v\n", c.cachedState)
		}

		if c.cachedState == configured {
			return true
		} else if c.cachedState == released {
			return false
		}
	}

	var err error
	c.floatingIP, _, err = c.client.FloatingIP.Get(context.Background(), c.config.HetznerCloudIpId)

	if c.verbose {
		log.Printf("queryAddress: state=%v floatingIp=%+v\n", c.cachedState, c.floatingIP)
	}

	if err != nil {
		if c.verbose {
			log.Printf("queryAddress: failed to query floating ip: %v\n", err)
		}

		c.cachedState = unknown
		return false
	} else {
		c.lastAPICheck = time.Now()
	}

	if c.floatingIP.Server != nil && c.floatingIP.Server.ID == c.server.ID {
		// We "are" the current failover destination.
		c.cachedState = configured
		return true
	}

	c.cachedState = released
	return false
}

func (c *HetznerCloudConfigurer) configureAddress() bool {
	if c.verbose {
		log.Printf("configuring floating ip %s on server %s", c.config.HetznerCloudIpId, c.server.Name)
	}

	if c.floatingIP == nil {
		log.Println("failed to assign floating ip: floating ip not found")
		return false
	}

	action, _, err := c.client.FloatingIP.Assign(context.Background(), c.floatingIP, c.server)
	if err != nil {
		log.Printf("failed to assign floating ip: %v\n", err)
		return false
	}

	progressCh, errCh := c.client.Action.WatchProgress(context.Background(), action)

	for {
		select {
		case progress := <-progressCh:
			if c.verbose {
				log.Printf("configureAddress: progress=%d\n", progress)
			}

			if progress == 100 {
				c.cachedState = configured
				return true
			}

			break

		case err := <-errCh:
			if err == nil {
				// Indicates also the action was successful
				c.cachedState = configured
				return true
			}

			c.cachedState = unknown
			log.Printf("failed to assign floating ip (action): %v\n", err)
			return false
		}
	}
}

func (c *HetznerCloudConfigurer) deconfigureAddress() bool {
	if c.verbose {
		log.Printf("deconfiguring floating ip %s on server %s", c.config.HetznerCloudIpId, c.server.Name)
	}

	if c.floatingIP == nil {
		log.Println("failed to unassign floating ip: floating ip not found")
		return false
	}

	action, _, err := c.client.FloatingIP.Unassign(context.Background(), c.floatingIP)
	if err != nil {
		log.Printf("failed to unassign floating ip: %v\n", err)
		return false
	}

	progressCh, errCh := c.client.Action.WatchProgress(context.Background(), action)

	for {
		select {
		case progress := <-progressCh:
			if c.verbose {
				log.Printf("deconfigureAddress: progress=%d\n", progress)
			}

			if progress == 100 {
				c.cachedState = released
				return true
			}
			break

		case err := <-errCh:
			if err == nil {
				// Indicates also the action was successful
				c.cachedState = released
				return true
			}

			c.cachedState = unknown
			log.Printf("failed to unassign floating ip (action): %v\n", err)
			return false
		}
	}
}

func (c *HetznerCloudConfigurer) cleanupArp() {
	// dummy function as the usage of interfaces requires us to have this function.
	// It is sufficient for the leader to tell Hetzner to switch the IP, no cleanup needed.
}

func (c *HetznerCloudConfigurer) getCIDR() string {
	if c.floatingIP == nil {
		return fmt.Sprintf("<unknown>/<unknown>")
	}

	mask := floatingIPMask(c.floatingIP)
	return fmt.Sprintf("%s/%d", c.floatingIP.IP.String(), netmaskSize(mask))
}

func (c *HetznerCloudConfigurer) createHetznerClient() error {
	c.client = hcloud.NewClient(hcloud.WithToken(c.config.HetznerCloudToken))
	if c.client == nil {
		return errors.New("Failed to connect to hetzner cloud")
	}

	server, _, err := c.client.Server.Get(context.Background(), c.config.HetznerCloudServerId)
	if err != nil {
		return err
	}

	c.server = server
	return nil
}

func floatingIPMask(fip *hcloud.FloatingIP) net.IPMask {
	if fip.Network != nil {
		return fip.Network.Mask
	}

	return fip.IP.DefaultMask()
}
