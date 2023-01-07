package rangeredisplugin

import (
	"context"
	"encoding/json"
	"net"
	"time"

	"github.com/go-redis/redis/v9"
)

const REDIS_KEY_PREFIX = "dhcp:"
const REDIS_SHADOW_KEY_PREFIX = "s:dhcp:"

// Record holds an IP lease record
type Record struct {
	IP      net.IP
	Expires time.Time
}

type RedisProvider struct {
	rdb    *redis.Client
	SubExp *redis.PubSub
}

// Establish connection with Redis. The connStr should be in format
// "redis://<user>:<pass>@localhost:6379/<db>"
func InitStorage(connStr string) (*RedisProvider, error) {
	r := &RedisProvider{}

	opt, err := redis.ParseURL(connStr)
	if err != nil {
		return nil, err
	}

	r.rdb = redis.NewClient(opt)
	_, err = r.rdb.Ping(context.TODO()).Result()
	if err != nil {
		return nil, err
	}

	// subscribe to expire info
	r.SubExp = r.rdb.Subscribe(context.TODO(), "__keyevent@0__:expired")

	log.Infof("set storage to %s", connStr)
	return r, nil
}

// Get Record from Redis. Records are identified by MAC address and a prefix.
func (r *RedisProvider) GetRecord(mac string) (*Record, error) {
	record := Record{}

	val, err := r.rdb.Get(context.TODO(), REDIS_KEY_PREFIX+mac).Result()
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
	keys, err := r.rdb.Keys(context.TODO(), REDIS_KEY_PREFIX+"*").Result()
	if err != nil {
		if err == redis.Nil {
			return &[]Record{}, nil
		}
		return nil, err
	}

	records := make([]Record, 0, len(keys))
	for _, key := range keys {
		record, err := r.GetRecord(key[len(REDIS_KEY_PREFIX):])
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

	// set the actual key with extra ttl 10s
	err = r.rdb.Set(context.TODO(),
		REDIS_KEY_PREFIX+mac.String(), string(recBytes),
		time.Until(record.Expires.Add(10*time.Second)).Round(time.Second)).Err()
	if err != nil {
		return err
	}

	// set the shadow key to receive notification
	err = r.rdb.Set(context.TODO(),
		REDIS_SHADOW_KEY_PREFIX+mac.String(), "",
		time.Until(record.Expires).Round(time.Second)).Err()

	return err
}
