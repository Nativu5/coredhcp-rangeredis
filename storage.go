package rangeredisplugin

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/go-redis/redis/v9"
)

// Record holds an IP lease record
type Record struct {
	IP      net.IP
	Expires time.Time
}

type StorageProvider interface {
	Init(string) (StorageProvider, error)
	GetRecord(string) (*Record, error)
	GetAllRecords() (*[]Record, error)
	SaveIPAddress(net.HardwareAddr, *Record) error
}

func ParseURI(uri string) (StorageProvider, error) {
	strs := strings.Split(uri, "://")
	if len(strs) < 2 {
		return nil, errors.New("malformed uri: " + uri)
	}

	protocol := strs[0]
	// location := strings.Join(strs[1:], "://")

	switch protocol {
	case "redis":
		return (&RedisProvider{}).Init(uri)
		// case "file":
		// 	return (&FileProvider{}).Init(location) // TODO
	}

	return nil, errors.New("unknown protocol: " + uri)
}

type RedisProvider struct {
	rdb    *redis.Client
	Prefix string // key prefix
}

// Establish connection with Redis. The connStr should be in format
// "redis://<user>:<pass>@localhost:6379/<db>"
func (r *RedisProvider) Init(connStr string) (StorageProvider, error) {
	// set prefix for keys
	r.Prefix = "mac:"

	opt, err := redis.ParseURL(connStr)
	if err != nil {
		return nil, err
	}

	r.rdb = redis.NewClient(opt)
	_, err = r.rdb.Ping(context.TODO()).Result()
	if err != nil {
		return nil, err
	}

	log.Infof("set storage to %s", connStr)
	return r, nil
}

// Get Record from Redis. Records are identified by MAC address and a prefix "mac:".
func (r *RedisProvider) GetRecord(mac string) (*Record, error) {
	record := Record{}

	val, err := r.rdb.Get(context.TODO(), r.Prefix+mac).Result()
	if err != nil {
		if err == redis.Nil {
			return &record, nil
		}
		return nil, err
	}

	if err = json.Unmarshal([]byte(val), &record); err != nil {
		return nil, err
	}

	return &record, nil
}

// Get all records from redis. Used in case the DHCP server is restarted.
func (r *RedisProvider) GetAllRecords() (*[]Record, error) {
	keys, err := r.rdb.Keys(context.TODO(), r.Prefix+"*").Result()
	if err != nil {
		if err == redis.Nil {
			return &[]Record{}, nil
		}
		return nil, err
	}

	records := make([]Record, 0, len(keys))
	for _, key := range keys {
		record, err := r.GetRecord(key[len(r.Prefix):])
		if err != nil || record.IP == nil {
			continue
		}

		records = append(records, *record)
	}

	return &records, nil
}

func (r *RedisProvider) SaveIPAddress(mac net.HardwareAddr, record *Record) error {
	recBytes, err := json.Marshal(record)
	if err != nil {
		return err
	}

	err = r.rdb.Set(context.TODO(),
		r.Prefix+mac.String(), string(recBytes), time.Until(record.Expires).Round(time.Second)).Err()
	return err
}
