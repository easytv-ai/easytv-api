package common

// PageInfo 分页参数（兼容参考项目 common.PageInfo）。
type PageInfo struct {
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

// GetPageSize 返回分页大小（默认 10，上限 100）。
func (p *PageInfo) GetPageSize() int {
	if p == nil || p.PageSize <= 0 {
		return 10
	}
	if p.PageSize > 100 {
		return 100
	}
	return p.PageSize
}

// GetStartIdx 返回 offset。
func (p *PageInfo) GetStartIdx() int {
	if p == nil || p.Page <= 1 {
		return 0
	}
	return (p.Page - 1) * p.GetPageSize()
}
