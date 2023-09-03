package utils

func MIMEs2Ext(mimes []string) string {
	ext := mimes[0]
	for _, mime := range mimes {
		switch mime {
		case ".jpg":
			ext = ".jpg"
		case ".png":
			ext = ".png"
		case ".gif":
			ext = ".gif"
		case ".mp4":
			ext = ".mp4"
		}
	}

	return ext
}
