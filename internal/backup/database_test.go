package backup

import (
	"reflect"
	"testing"

	"github.com/scootec/rdb/internal/docker"
)

func TestBuildDBFilename(t *testing.T) {
	tests := []struct {
		name   string
		ctr    docker.ContainerInfo
		dbType string
		want   string
	}{
		{
			name:   "project and service present",
			ctr:    docker.ContainerInfo{Name: "myapp-db-1", Project: "myapp", Service: "db"},
			dbType: "postgres",
			want:   "databases/myapp/db/postgres/all_databases.sql",
		},
		{
			name:   "no project omits the project segment",
			ctr:    docker.ContainerInfo{Name: "standalone-db", Service: "db"},
			dbType: "mysql",
			want:   "databases/db/mysql/all_databases.sql",
		},
		{
			name:   "no service falls back to container name",
			ctr:    docker.ContainerInfo{Name: "standalone-db", Project: "myapp"},
			dbType: "mariadb",
			want:   "databases/myapp/standalone-db/mariadb/all_databases.sql",
		},
		{
			name:   "neither project nor service",
			ctr:    docker.ContainerInfo{Name: "plain"},
			dbType: "postgres",
			want:   "databases/plain/postgres/all_databases.sql",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildDBFilename(tt.ctr, tt.dbType); got != tt.want {
				t.Errorf("buildDBFilename() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildTags(t *testing.T) {
	tests := []struct {
		name      string
		ctr       docker.ContainerInfo
		component string
		want      []string
	}{
		{
			name:      "project and service",
			ctr:       docker.ContainerInfo{Name: "myapp-db-1", Project: "myapp", Service: "db"},
			component: "postgres",
			want:      []string{"rdb", "postgres", "project:myapp", "service:db"},
		},
		{
			name:      "no project omits project tag",
			ctr:       docker.ContainerInfo{Name: "standalone", Service: "db"},
			component: "volume",
			want:      []string{"rdb", "volume", "service:db"},
		},
		{
			name:      "no service falls back to container name",
			ctr:       docker.ContainerInfo{Name: "standalone"},
			component: "volume",
			want:      []string{"rdb", "volume", "service:standalone"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildTags(tt.ctr, tt.component); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildTags() = %v, want %v", got, tt.want)
			}
		})
	}
}
