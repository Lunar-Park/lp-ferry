package preset

// Built-in preset IDs.
const (
	BuiltinPreserve = "preserve-source-structure"
	BuiltinCategory = "sort-by-category"
	BuiltinCamera   = "camera-card-conform"
	BuiltinMirror   = "mirror-source"
)

func yes() *bool { b := true; return &b }

// Builtins are the starter presets. None routes by date by default except
// camera-card-conform, whose whole purpose is the embedded capture date —
// and which falls back visibly when a file has none.
func Builtins() map[string]*Preset {
	return map[string]*Preset{
		BuiltinPreserve: {
			Schema: Schema, Name: "Preserve source structure",
			Description:    "Keep each drive or folder's layout intact inside its own source folder.",
			Rules:          []Rule{{ID: "preserve", Template: "Sources/{source_label}/{relative_dir}/{filename}"}},
			Fallback:       "Sources/{source_label}/{relative_dir}/{filename}",
			EmptyDirs:      "Sources/{source_label}/{relative_dir}/{filename}",
			ConflictPolicy: KeepBoth,
		},
		BuiltinCategory: {
			Schema: Schema, Name: "Sort loose files by category",
			Description: "Separate loose video, audio, images, documents, and archives while keeping their source folders traceable.",
			Rules: []Rule{
				{ID: "category-video", When: Conditions{Category: []string{CatVideo}}, Template: "Video/{source_label}/{relative_dir}/{filename}"},
				{ID: "category-audio", When: Conditions{Category: []string{CatAudio}}, Template: "Audio/{source_label}/{relative_dir}/{filename}"},
				{ID: "category-images", When: Conditions{Category: []string{CatImage}}, Template: "Images/{source_label}/{relative_dir}/{filename}"},
				{ID: "category-documents", When: Conditions{Category: []string{CatDocument}}, Template: "Documents/{source_label}/{relative_dir}/{filename}"},
				{ID: "category-archives", When: Conditions{Category: []string{CatArchive}}, Template: "Archives/{source_label}/{relative_dir}/{filename}"},
			},
			Fallback:       "Unsorted/{source_label}/{relative_dir}/{filename}",
			EmptyDirs:      "Unsorted/{source_label}/{relative_dir}/{filename}",
			ConflictPolicy: KeepBoth,
		},
		BuiltinMirror: {
			Schema: Schema, Name: "Mirror source",
			Description:    "Recreate the source's folders directly inside the destination, nothing added.",
			Rules:          []Rule{{ID: "mirror", Template: "{relative_dir}/{filename}"}},
			Fallback:       "{relative_dir}/{filename}",
			EmptyDirs:      "{relative_dir}/{filename}",
			ConflictPolicy: KeepBoth,
		},
		BuiltinCamera: {
			Schema: Schema, Name: "Camera card conform",
			Description: "File clips by camera role and capture date, renamed with the role; vendor sidecars travel with their clip.",
			Rules: []Rule{
				{ID: "camera-clips", When: Conditions{Category: []string{CatVideo}}, Template: "{camera_role}/{capture_date}/{camera_role}_{filename}"},
				{ID: "camera-sidecars", When: Conditions{Companion: yes()}, Template: "{camera_role}/{capture_date}/{camera_role}_{filename}"},
			},
			Fallback:       "Card/{source_label}/{relative_dir}/{filename}",
			EmptyDirs:      "Card/{source_label}/{relative_dir}/{filename}",
			ConflictPolicy: KeepBoth,
		},
	}
}
