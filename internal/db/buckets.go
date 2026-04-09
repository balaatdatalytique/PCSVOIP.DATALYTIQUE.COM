package db

// Bucket names used by the admin module. Each bucket is created on Open if missing.
var (
	BucketUsers    = []byte("users")     // key: username
	BucketSessions = []byte("sessions")  // key: session token
	BucketSettings = []byte("settings")  // key: "global"
	BucketBot      = []byte("bot")       // key: "global"
	BucketKB       = []byte("kb")        // key: ulid
	BucketVisitors = []byte("visitors")  // key: visitor id (sha1[:16] of ip+ua)
	BucketEvents   = []byte("events")    // key: ulid (lex-orderable by time)
	BucketGeoCache = []byte("geo_cache") // key: ip address, value: GeoInfo
)

var allBuckets = [][]byte{
	BucketUsers, BucketSessions, BucketSettings, BucketBot,
	BucketKB, BucketVisitors, BucketEvents, BucketGeoCache,
}
