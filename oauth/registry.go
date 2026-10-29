package oauth

import "sync"

var (
	providers = make(map[string]Provider)
	mu        sync.RWMutex
)

// Register 注册 OAuth provider（按 slug）。
func Register(provider Provider) {
	mu.Lock()
	defer mu.Unlock()
	providers[provider.GetSlug()] = provider
}

// GetProvider 按 slug 获取 provider。
func GetProvider(slug string) Provider {
	mu.RLock()
	defer mu.RUnlock()
	return providers[slug]
}

// GetAllProviders 返回全部已注册 provider。
func GetAllProviders() map[string]Provider {
	mu.RLock()
	defer mu.RUnlock()
	result := make(map[string]Provider, len(providers))
	for k, v := range providers {
		result[k] = v
	}
	return result
}

// GetEnabledProviders 返回已启用的 provider（用于登录页展示）。
func GetEnabledProviders() []Provider {
	mu.RLock()
	defer mu.RUnlock()
	var result []Provider
	for _, p := range providers {
		if p.IsEnabled() {
			result = append(result, p)
		}
	}
	return result
}

// IsProviderRegistered 判断 provider 是否已注册。
func IsProviderRegistered(slug string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := providers[slug]
	return ok
}
