package rangeredisplugin

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/coredhcp/coredhcp/handler"
	"github.com/coredhcp/coredhcp/logger"
	"github.com/coredhcp/coredhcp/plugins"
	"github.com/coredhcp/coredhcp/plugins/allocators"
	"github.com/coredhcp/coredhcp/plugins/allocators/bitmap"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv6"
)

var log = logger.GetLogger("plugins/range-redis")

// Note that importing the plugin is not enough to use it: you have to
// explicitly specify the intention to use it in the `config.yml` file, in the
// plugins section. For example:
//
// server6:
//
//	listen: '[::]547'
//	- example:
//	- server_id: LL aa:bb:cc:dd:ee:ff
//	- file: "leases.txt"

var Plugin = plugins.Plugin{
	Name:   "range-redis",
	Setup6: setup6,
	Setup4: setup4,
}

// PluginState is the data held by an instance of the range plugin
type PluginState struct {
	LeaseTime time.Duration
	storage   *RedisProvider
	allocator allocators.Allocator
}

// Handler4 handles DHCPv4 packets for the range plugin
func (p *PluginState) Handler4(req, resp *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, bool) {
	record, err := p.storage.GetRecord(req.ClientHWAddr.String())
	if err != nil {
		log.Errorf("Could not get record for %s: %v", req.ClientHWAddr.String(), err)
		return nil, true
	}

	if record.IP == nil {
		// Allocating new address since there isn't one allocated
		log.Printf("MAC address %s is new, leasing new IPv4 address", req.ClientHWAddr.String())
		ip, err := p.allocator.Allocate(net.IPNet{})
		if err != nil {
			log.Errorf("Could not allocate IP for MAC %s: %v", req.ClientHWAddr.String(), err)
			return nil, true
		}
		rec := Record{
			IP:      ip.IP.To4(),
			Expires: time.Now().Add(p.LeaseTime),
		}
		err = p.storage.SaveIPAddress(req.ClientHWAddr, &rec)
		if err != nil {
			log.Errorf("SaveIPAddress for MAC %s failed: %v", req.ClientHWAddr.String(), err)
		}
		record = &rec
	} else {
		// Ensure we extend the existing lease at least past when the one we're giving expires
		if record.Expires.Before(time.Now().Add(p.LeaseTime)) {
			record.Expires = time.Now().Add(p.LeaseTime).Round(time.Second)
			err := p.storage.SaveIPAddress(req.ClientHWAddr, record)
			if err != nil {
				log.Errorf("Could not persist lease for MAC %s: %v", req.ClientHWAddr.String(), err)
			}
		}
	}
	resp.YourIPAddr = record.IP
	resp.Options.Update(dhcpv4.OptIPAddressLeaseTime(p.LeaseTime.Round(time.Second)))
	log.Printf("found IP address %s for MAC %s", record.IP, req.ClientHWAddr.String())
	return resp, false
}

func setup4(args ...string) (handler.Handler4, error) {
	var (
		err error
		p   PluginState
	)

	if len(args) < 4 {
		return nil, fmt.Errorf("invalid number of arguments, want: 4 (uri, start IP, end IP, lease time), got: %d", len(args))
	}
	uri := args[0]
	if uri == "" {
		return nil, errors.New("uri cannot be empty")
	}
	ipRangeStart := net.ParseIP(args[1])
	if ipRangeStart.To4() == nil {
		return nil, fmt.Errorf("invalid IPv4 address: %v", args[1])
	}
	ipRangeEnd := net.ParseIP(args[2])
	if ipRangeEnd.To4() == nil {
		return nil, fmt.Errorf("invalid IPv4 address: %v", args[2])
	}
	if binary.BigEndian.Uint32(ipRangeStart.To4()) >= binary.BigEndian.Uint32(ipRangeEnd.To4()) {
		return nil, errors.New("start of IP range has to be lower than the end of an IP range")
	}

	p.allocator, err = bitmap.NewIPv4Allocator(ipRangeStart, ipRangeEnd)
	if err != nil {
		return nil, fmt.Errorf("could not create an allocator: %w", err)
	}

	p.LeaseTime, err = time.ParseDuration(args[3])
	if err != nil {
		return nil, fmt.Errorf("invalid lease duration: %v", args[3])
	}

	p.storage, err = InitStorage(uri)
	if err != nil {
		return nil, err
	}

	records, err := p.storage.GetAllRecords()
	if err != nil {
		return nil, fmt.Errorf("could not load records: %v", err)
	}

	log.Printf("Loaded %d DHCPv4 leases from %s", len(*records), uri)

	for _, v := range *records {
		ip, err := p.allocator.Allocate(net.IPNet{IP: v.IP})
		if err != nil {
			return nil, fmt.Errorf("failed to re-allocate leased ip %v: %v", v.IP.String(), err)
		}
		if ip.IP.String() != v.IP.String() {
			return nil, fmt.Errorf("allocator did not re-allocate requested leased ip %v: %v", v.IP.String(), ip.String())
		}
	}

	// Launch a goroutine to gc the IP lease
	go func() {
		ch := p.storage.SubExp.Channel()
		defer p.storage.SubExp.Close()

		for msg := range ch {
			if !strings.HasPrefix(msg.Payload, REDIS_SHADOW_KEY_PREFIX) {
				continue
			}

			mac := msg.Payload[len(REDIS_SHADOW_KEY_PREFIX):]
			record, err := p.storage.GetRecord(mac)
			if err != nil {
				log.Errorln("error when getting expired record", err)
				continue
			}

			err = p.allocator.Free(net.IPNet{
				IP:   record.IP,
				Mask: net.IPv4Mask(255, 255, 255, 255),
			})

			if err != nil {
				log.Errorf("error when release ip %v, err: %v", record.IP, err)
				continue
			}

			log.Infof("IP lease %s for MAC address %s is expire.", record.IP, mac)
		}
	}()

	return p.Handler4, nil
}

// Handler6 handles DHCPv6 packets for the plugin.
func Handler6(req, resp dhcpv6.DHCPv6) (dhcpv6.DHCPv6, bool) {
	log.Warnf("skipped DHCPv6 packet: %s", req.Summary())
	// return the unmodified response, and false. This means that the next
	// plugin in the chain will be called, and the unmodified response packet
	// will be used as its input.
	return resp, false
}

// setup6 is the setup function to initialize the handler for DHCPv6
// traffic.
func setup6(args ...string) (handler.Handler6, error) {
	log.Warn("this plugin currently does not support DHCPv6.")
	return Handler6, nil
}
