package config

import (
	"fmt"
	"github.com/spf13/viper"
)

type Config struct {
	Server   ServerConfig   `mapstructure:"Server"`
	Database DatabaseConfig `mapstructure:"Database"`
}

type ServerConfig struct {
	Port     string `mapstructure:"Port"`
	BaseURL  string `mapstructure:"BaseURL"`
	VideoDir string `mapstructure:"VideoDir"`
	GRPCPort string `mapstructure:"GRPCPort"`
}

type DatabaseConfig struct {
	Host     string `mapstructure:"Host"`
	Port     string `mapstructure:"Port"`
	User     string `mapstructure:"User"`
	Password string `mapstructure:"Password"`
	Name     string `mapstructure:"Name"`
	SSLMode  string `mapstructure:"SSLMode"`
}

func NewConfig(path string) (*Config, error) {
	v := viper.New()

	// Устанавливаем файл конфигурации
	v.SetConfigFile(path)
	v.SetEnvPrefix("DATABASE") // Устанавливаем префикс для переменных окружения

	// Привязываем переменные окружения
	v.BindEnv("Database.Host", "DATABASE_HOST")
	v.BindEnv("Database.Port", "DATABASE_PORT")
	v.BindEnv("Database.User", "DATABASE_USER")
	v.BindEnv("Database.Password", "DATABASE_PASSWORD")
	v.BindEnv("Database.Name", "DATABASE_NAME")
	v.BindEnv("Database.SSLMode", "DATABASE_SSLMODE")
	v.BindEnv("Server.Port", "HTTP_PORT")

	// Читаем конфигурацию из файла
	if err := v.ReadInConfig(); err != nil {
		fmt.Printf("Warning: using only environment variables: %v\n", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Проверяем переменные окружения напрямую если конфигурация неполная
	if cfg.Database.Host == "" {
		cfg.Database.Host = v.GetString("DATABASE_HOST")
	}
	if cfg.Database.Port == "" {
		cfg.Database.Port = v.GetString("DATABASE_PORT")
	}
	if cfg.Database.User == "" {
		cfg.Database.User = v.GetString("DATABASE_USER")
	}
	if cfg.Database.Password == "" {
		cfg.Database.Password = v.GetString("DATABASE_PASSWORD")
	}
	if cfg.Database.Name == "" {
		cfg.Database.Name = v.GetString("DATABASE_NAME")
	}
	if cfg.Database.SSLMode == "" {
		cfg.Database.SSLMode = v.GetString("DATABASE_SSLMODE")
	}
	if cfg.Server.Port == "" {
		cfg.Server.Port = v.GetString("HTTP_PORT")
	}
	if cfg.Server.VideoDir == "" {
		cfg.Server.VideoDir = "/tmp/videos" // Значение по умолчанию
	}

	// Проверяем, что все необходимые поля заполнены
	if cfg.Database.Host == "" ||
		cfg.Database.Port == "" ||
		cfg.Database.User == "" ||
		cfg.Database.Password == "" ||
		cfg.Database.Name == "" {
		return nil, fmt.Errorf("database configuration is incomplete: host=%s, port=%s, user=%s, name=%s",
			cfg.Database.Host, cfg.Database.Port, cfg.Database.User, cfg.Database.Name)
	}

	v.BindEnv("Server.GRPCPort", "GRPC_PORT")

	// Установка значений по умолчанию
	if cfg.Database.SSLMode == "" {
		cfg.Database.SSLMode = "disable"
	}
	if cfg.Server.Port == "" {
		cfg.Server.Port = "2525"
	}

	if cfg.Server.GRPCPort == "" {
		cfg.Server.GRPCPort = "50051"
	}

	return &cfg, nil
}

func (c *DatabaseConfig) GetDSN() string {
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.Host,
		c.Port,
		c.User,
		c.Password,
		c.Name,
		c.SSLMode,
	)
}
