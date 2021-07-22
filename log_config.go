package rzlog

type LogCfg struct {
	FilePath string `mapstructure:"file_path" json:"file_path" envconfig:"FILE_PATH" required:"false"`
	Level    string `mapstructure:"level" json:"level" envconfig:"LEVEL" required:"true" default:"info"`
	Format   string `mapstructure:"format" json:"format" envconfig:"FORMAT" required:"false" default:"json"`
	Rotation bool   `mapstructure:"rotation" json:"rotation" envconfig:"ROTATION" required:"false" default:"false"`
}
