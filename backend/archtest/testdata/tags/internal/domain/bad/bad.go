package bad

type Node struct {
	ID   string `json:"id"`
	Meta struct {
		Name string `db:"name"`
	}
}

type Clean struct{ ID string }
