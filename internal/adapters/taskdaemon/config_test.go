package taskdaemon

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskDaemonConfig_ExposesRuntimeFieldsWithoutEnvTags(t *testing.T) {
	typ := reflect.TypeOf(Config{})

	fields := map[string]reflect.StructField{}
	for i := range typ.NumField() {
		field := typ.Field(i)
		fields[field.Name] = field
	}

	require.Contains(t, fields, "SocketPath")
	require.Contains(t, fields, "HookListenAddr")
	require.Contains(t, fields, "ExecPath")
	require.Contains(t, fields, "Env")

	require.NotEmpty(t, fields["SocketPath"].Tag.Get("env"))
	require.NotEmpty(t, fields["HookListenAddr"].Tag.Get("env"))
	require.Empty(t, fields["ExecPath"].Tag.Get("env"))
	require.Empty(t, fields["Env"].Tag.Get("env"))
}
