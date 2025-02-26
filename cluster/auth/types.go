package clusterauth

type Grant struct {
	Grant  string `json:"grant"`
	Enable bool   `json:"enable"`
}

type Role struct {
	Role   string `json:"role"`
	Enable bool   `json:"enable"`
}

type ListUserACL struct {
	User  string
	ACLs  string
	Roles string
}
