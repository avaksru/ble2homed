package history

import (
	"math"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// HistoryPoint — точка данных для истории
type HistoryPoint struct {
	Value     float64
	Timestamp time.Time
}

// HistoryRing — кольцевой буфер для хранения истории
type HistoryRing struct {
	Points []HistoryPoint
	Size   int
	Index  int
	Full   bool
	mu     sync.Mutex
}

// Manager — менеджер истории значений
type Manager struct {
	config  HistoryConfig
	logger  zerolog.Logger
	buffers map[string]map[string]*HistoryRing // mac -> field -> ring
	mu      sync.RWMutex
}

// HistoryConfig — настройки истории
type HistoryConfig struct {
	Enabled   bool
	Intervals []string
}

// NewManager — создание нового менеджера истории
func NewManager(config HistoryConfig, logger zerolog.Logger) *Manager {
	return &Manager{
		config:  config,
		logger:  logger.With().Str("component", "history").Logger(),
		buffers: make(map[string]map[string]*HistoryRing),
	}
}

// NewHistoryRing — создание нового кольцевого буфера
func NewHistoryRing(size int) *HistoryRing {
	return &HistoryRing{
		Points: make([]HistoryPoint, size),
		Size:   size,
		Index:  0,
		Full:   false,
	}
}

// Add — добавление точки в буфер
func (r *HistoryRing) Add(value float64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.Points[r.Index] = HistoryPoint{
		Value:     value,
		Timestamp: time.Now(),
	}

	r.Index = (r.Index + 1) % r.Size
	if r.Index == 0 {
		r.Full = true
	}
}

// GetAverage — получение среднего значения за период
func (r *HistoryRing) GetAverage(duration time.Duration) (float64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoff := time.Now().Add(-duration)
	var sum float64
	var count int

	for i := 0; i < r.Size; i++ {
		if !r.Full && i >= r.Index {
			break
		}

		point := r.Points[i]
		if point.Timestamp.After(cutoff) && !point.Timestamp.IsZero() {
			sum += point.Value
			count++
		}
	}

	if count == 0 {
		return 0, false
	}

	return math.Round((sum/float64(count))*100) / 100, true
}

// Cleanup — удаление старых точек
func (r *HistoryRing) Cleanup(cutoff time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i := 0; i < r.Size; i++ {
		if !r.Points[i].Timestamp.IsZero() && r.Points[i].Timestamp.Before(cutoff) {
			r.Points[i] = HistoryPoint{}
		}
	}
}

// Count — количество точек в буфере
func (r *HistoryRing) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	count := 0
	for i := 0; i < r.Size; i++ {
		if !r.Points[i].Timestamp.IsZero() {
			count++
		}
	}
	return count
}

// AddValue — добавление значения в историю
func (m *Manager) AddValue(mac, field string, value float64) {
	if !m.config.Enabled {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.buffers[mac]; !ok {
		m.buffers[mac] = make(map[string]*HistoryRing)
	}

	if _, ok := m.buffers[mac][field]; !ok {
		m.buffers[mac][field] = NewHistoryRing(8640) // 24 часа при интервале 10 секунд
	}

	m.buffers[mac][field].Add(value)
}

// GetAverage — получение среднего значения за интервал
func (m *Manager) GetAverage(mac, field string, interval string) (float64, bool) {
	if !m.config.Enabled {
		return 0, false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	macBuffers, ok := m.buffers[mac]
	if !ok {
		return 0, false
	}

	ring, ok := macBuffers[field]
	if !ok {
		return 0, false
	}

	duration := parseInterval(interval)
	if duration == 0 {
		return 0, false
	}

	return ring.GetAverage(duration)
}

// GetAllAverages — получение всех средних значений для устройства
func (m *Manager) GetAllAverages(mac string) map[string]map[string]float64 {
	if !m.config.Enabled {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]map[string]float64)

	macBuffers, ok := m.buffers[mac]
	if !ok {
		return result
	}

	for field, ring := range macBuffers {
		fieldAverages := make(map[string]float64)
		for _, interval := range m.config.Intervals {
			duration := parseInterval(interval)
			if duration > 0 {
				if avg, ok := ring.GetAverage(duration); ok {
					fieldAverages[interval] = avg
				}
			}
		}
		if len(fieldAverages) > 0 {
			result[field] = fieldAverages
		}
	}

	return result
}

// Cleanup — очистка старых данных
func (m *Manager) Cleanup(maxAge time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)

	for mac, fields := range m.buffers {
		for _, ring := range fields {
			ring.Cleanup(cutoff)
		}

		// Удаляем устройства без данных
		hasData := false
		for _, ring := range fields {
			if ring.Count() > 0 {
				hasData = true
				break
			}
		}
		if !hasData {
			delete(m.buffers, mac)
		}
	}
}

// parseInterval — парсинг строкового интервала в time.Duration
func parseInterval(interval string) time.Duration {
	switch interval {
	case "1m":
		return time.Minute
	case "10m":
		return 10 * time.Minute
	case "1h":
		return time.Hour
	case "24h":
		return 24 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	default:
		return 0
	}
}
