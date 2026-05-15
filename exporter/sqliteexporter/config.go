package sqliteexporter

type Config struct {
	Path string `mapstructure:"path"`
}

func (c *Config) Validate() error {
	return nil
}
