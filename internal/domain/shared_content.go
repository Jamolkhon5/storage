package domain

// SharedContent представляет контент с общим доступом
type SharedContent struct {
	Share         Share        `json:"share"`
	ResourceType  ResourceType `json:"resource_type"`
	AccessType    AccessType   `json:"access_type"`
	Path          string       `json:"path"` // Путь в контексте общего доступа
	CurrentFolder *Folder      `json:"current_folder,omitempty"`
	ParentFolders []Folder     `json:"parent_folders,omitempty"`
	Files         []File       `json:"files,omitempty"`
	Subfolders    []Folder     `json:"subfolders,omitempty"`
}

// SharedBreadcrumb представляет элемент навигации для общего доступа
type SharedBreadcrumb struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	ShareID string `json:"share_id"`
	IsRoot  bool   `json:"is_root"`
}
