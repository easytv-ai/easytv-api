package common

import (
	"context"
	"errors"
	"time"

	"github.com/go-redis/redis/v8"
)

var (
	RedisClient *redis.Client
	RedisCtx    = context.Background()
)

// InitRedisClient 初始化 Redis 客户端（REDIS_CONN_STRING 格式：redis://user:pass@host:port/db）。
func InitRedisClient() error {
	connString := GetEnvOrDefaultString("REDIS_CONN_STRING", "redis://localhost:6379/0")
	opt, err := redis.ParseURL(connString)
	if err != nil {
		SysError("failed to parse REDIS_CONN_STRING: " + err.Error())
		return err
	}
	RedisClient = redis.NewClient(opt)
	if err := RedisClient.Ping(RedisCtx).Err(); err != nil {
		SysError("failed to connect redis: " + err.Error())
		return err
	}
	SysLog("redis connected")
	return nil
}

// RedisSetEX 写入带过期时间的 key。Redis 未初始化时返回错误（由调用方降级处理）。
func RedisSetEX(key string, value any, expiration time.Duration) error {
	if RedisClient == nil {
		return errors.New("redis not initialized")
	}
	return RedisClient.Set(RedisCtx, key, value, expiration).Err()
}

// RedisGet 读取 key 值，不存在返回空字符串。Redis 未初始化时返回空。
func RedisGet(key string) string {
	if RedisClient == nil {
		return ""
	}
	return RedisClient.Get(RedisCtx, key).Val()
}

// RedisDel 删除一个或多个 key。Redis 未初始化时静默忽略。
func RedisDel(keys ...string) error {
	if RedisClient == nil {
		return nil
	}
	return RedisClient.Del(RedisCtx, keys...).Err()
}

// RedisIncr 自增并返回新值。Redis 未初始化时返回错误。
func RedisIncr(key string) (int64, error) {
	if RedisClient == nil {
		return 0, errors.New("redis not initialized")
	}
	return RedisClient.Incr(RedisCtx, key).Result()
}

// RedisTTL 返回 key 剩余秒数（-1 无过期，-2 不存在）。Redis 未初始化时返回 -2。
func RedisTTL(key string) time.Duration {
	if RedisClient == nil {
		return -2 * time.Second
	}
	return RedisClient.TTL(RedisCtx, key).Val()
}
