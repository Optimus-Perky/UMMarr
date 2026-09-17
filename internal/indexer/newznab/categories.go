package newznab

// Category is a Newznab standard category, e.g. {2040, "Movies/HD"}.
type Category struct {
	ID   int
	Name string
}

// Media types a category (and so an indexer) can serve.
const (
	MediaMovie  = "movie"
	MediaSeries = "series"
	MediaMusic  = "music"
)

// StandardCategories are the Newznab categories UMMarr offers, in the order
// Radarr, Sonarr and Lidarr list them.
var StandardCategories = []Category{
	{2000, "Movies"}, {2010, "Movies/Foreign"}, {2020, "Movies/Other"}, {2030, "Movies/SD"},
	{2040, "Movies/HD"}, {2045, "Movies/UHD"}, {2050, "Movies/BluRay"}, {2060, "Movies/3D"},
	{2070, "Movies/DVD"}, {2080, "Movies/WEB-DL"}, {2090, "Movies/x265"},
	{5000, "TV"}, {5010, "TV/WEB-DL"}, {5020, "TV/Foreign"}, {5030, "TV/SD"}, {5040, "TV/HD"},
	{5045, "TV/UHD"}, {5050, "TV/Other"}, {5060, "TV/Sport"}, {5070, "TV/Anime"},
	{5080, "TV/Documentary"}, {5090, "TV/x265"},
	{3000, "Audio"}, {3010, "Audio/MP3"}, {3020, "Audio/Video"}, {3030, "Audio/Audiobook"},
	{3040, "Audio/Lossless"}, {3050, "Audio/Other"}, {3060, "Audio/Foreign"},
}

// AnimeCategory is Sonarr's default anime category.
const AnimeCategory = 5070

// SyncCategories are the categories Prowlarr's Radarr, Sonarr and Lidarr apps
// sync by default, per media type (Sonarr's anime category is separate).
var SyncCategories = map[string][]int{
	MediaMovie:  {2000, 2010, 2020, 2030, 2040, 2045, 2050, 2060, 2070, 2080, 2090},
	MediaSeries: {5000, 5010, 5020, 5030, 5040, 5045, 5050, 5090},
	MediaMusic:  {3000, 3010, 3030, 3040, 3050, 3060},
}

// CategoryName is id's standard name, or "" for a category outside the
// standard list (indexers add their own, numbered 100000 and up).
func CategoryName(id int) string {
	for _, c := range StandardCategories {
		if c.ID == id {
			return c.Name
		}
	}
	return ""
}

// MediaTypeOf is the media type a standard category belongs to, or "".
func MediaTypeOf(id int) string {
	switch id / 1000 {
	case 2:
		return MediaMovie
	case 5:
		return MediaSeries
	case 3:
		return MediaMusic
	}
	return ""
}

// CategoriesFor keeps the categories in ids that belong to mediaType.
func CategoriesFor(ids []int, mediaType string) []int {
	var out []int
	for _, id := range ids {
		if MediaTypeOf(id) == mediaType {
			out = append(out, id)
		}
	}
	return out
}
