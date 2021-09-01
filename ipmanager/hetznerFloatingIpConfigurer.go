package ipmanager

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"

	"github.com/hetznercloud/hcloud-go/hcloud"
)

type HetznerFloatingIpConfigurer struct {
	*IPConfiguration
	client       *hcloud.Client
	server       *hcloud.Server
	floatingIpId string
	cachedState  int
	lastAPICheck time.Time
	verbose      bool
}

func newHetznerFloatingIpConfigurer(config *vipconfig.Config, ipConfig *IPConfiguration) (*HetznerFloatingIpConfigurer, error) {
	client := hcloud.NewClient(hcloud.WithToken(config.HetznerCloudToken))
	if client == nil {
		return nil, errors.New("Failed to connect to hetzner cloud")
	}

	server, _, err := client.Server.Get(context.Background(), config.HetznerCloudServerId)
	if err != nil {
		return nil, err
	}

	c := &HetznerFloatingIpConfigurer{
		IPConfiguration: ipConfig,
		client:          client,
		server:          server,
		floatingIpId:    config.HetznerCloudIpId,
		cachedState:     unknown,
		lastAPICheck:    time.Unix(0, 0),
		verbose:         config.Verbose,
	}

	return c, nil
}

func (c *HetznerFloatingIpConfigurer) queryAddress() bool {
	if (time.Since(c.lastAPICheck) / time.Second) > 1 {
		/**We need to recheck the status!
		 * Don't check too often because of stupid API rate limits
		 */
		log.Println("Cached state was too old.")
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

	floatingIp, _, err := c.client.FloatingIP.Get(context.Background(), c.floatingIpId)

	if c.verbose {
		log.Printf("queryAddress: state=%v floatingIp=%+v\n", c.cachedState, floatingIp)
	}

	if err != nil {
		if c.verbose {
			log.Printf("queryAddress: err=%v\n", err)
		}

		c.cachedState = unknown
		return false
	} else {
		c.lastAPICheck = time.Now()
	}

	if floatingIp.Server != nil && floatingIp.Server.ID == c.server.ID {
		// We "are" the current failover destination.
		c.cachedState = configured
		return true
	}

	c.cachedState = released
	return false
}

func (c *HetznerFloatingIpConfigurer) configureAddress() bool {
	if c.verbose {
		log.Printf("configuring floating ip %s on server %s", c.floatingIpId, c.server.Name)
	}

	floatingIp, _, err := c.client.FloatingIP.Get(context.Background(), c.floatingIpId)
	if err != nil {
		log.Printf("failed to query floating ip: %v\n", err)
		return false
	}

	action, _, err := c.client.FloatingIP.Assign(context.Background(), floatingIp, c.server)
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

func (c *HetznerFloatingIpConfigurer) deconfigureAddress() bool {
	if c.verbose {
		log.Printf("deconfiguring floating ip %s on server %s", c.floatingIpId, c.server.Name)
	}

	floatingIp, _, err := c.client.FloatingIP.Get(context.Background(), c.floatingIpId)
	if err != nil {
		log.Printf("failed to query floating ip: %v\n", err)
		return false
	}

	action, _, err := c.client.FloatingIP.Unassign(context.Background(), floatingIp)
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

func (c *HetznerFloatingIpConfigurer) cleanupArp() {
	// dummy function as the usage of interfaces requires us to have this function.
	// It is sufficient for the leader to tell Hetzner to switch the IP, no cleanup needed.
}
