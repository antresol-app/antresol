package auth

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // uploaded photos
	"strconv"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // uploaded photos
)

// A profile's photo: at most MaxPhotoBytes of PNG, JPEG or WebP, cut to its
// middle square, fitted into PhotoBox×PhotoBox (a smaller one keeps its size)
// and kept as JPEG in user_photos. users.photo_version names the upload.
const (
	MaxPhotoBytes = 8 << 20
	PhotoBox      = 512
	// photoQuality: a face at 512 px is about 40 kB, and no block shows at the
	// 132 pt it is drawn at most (a TV's profile picker).
	photoQuality = 88
	// maxPhotoPixels refuses images that would take too much memory to
	// decode (a small file can claim a huge picture).
	maxPhotoPixels = 50_000_000
)

// ErrPhotoFormat: not a PNG, JPEG or WebP image (or a broken one).
var ErrPhotoFormat = errors.New("auth: not a PNG, JPEG or WebP image")

// FitPhoto decodes an uploaded photo, cuts the middle square out of it, fits
// that into PhotoBox×PhotoBox and returns it as JPEG. A picture with
// transparency is laid on a neutral ground (a JPEG has none). The web app
// sends the square the person chose already; this is for clients that send
// a camera's picture whole. The picture's EXIF orientation is not read: a
// client sends it upright.
func FitPhoto(data []byte) ([]byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg" && format != "webp") {
		return nil, ErrPhotoFormat
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxPhotoPixels {
		return nil, ErrPhotoFormat
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrPhotoFormat
	}
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	// (image.Rect puts its corners in order, so a rectangle with a placeholder corner is not this one)
	x, y := b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2
	sq := image.Rectangle{Min: image.Pt(x, y), Max: image.Pt(x+side, y+side)}
	out := min(side, PhotoBox)
	dst := image.NewRGBA(image.Rect(0, 0, out, out))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.RGBA{R: 0x2c, G: 0x2c, B: 0x2e, A: 0xff}), image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, sq, xdraw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: photoQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// PhotoURL is the address of profile id's photo, "" without one. The version
// is in the query, so a new upload is a new address and caches never keep the
// old face. The address is public, as the profile list (/api/profiles) that
// names the profile is: the sign-in screen shows it before anyone signs in.
func PhotoURL(id, version int64) string {
	if version == 0 {
		return ""
	}
	return "/api/profiles/" + strconv.FormatInt(id, 10) + "/photo?v=" + strconv.FormatInt(version, 10)
}

// SetPhoto keeps jpegData (FitPhoto's) as profile id's photo and returns the
// profile. The version is the time of the upload, and always above the last.
func (s *Store) SetPhoto(ctx context.Context, id int64, jpegData []byte) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var old int64
	if err := tx.QueryRowContext(ctx, `SELECT photo_version FROM users WHERE id=?`, id).Scan(&old); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_photos(user_id, data) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET data=excluded.data`, id, jpegData); err != nil {
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET photo_version=? WHERE id=?`, max(time.Now().Unix(), old+1), id); err != nil {
		return User{}, err
	}
	u, err := scan(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM users WHERE id=?`, id))
	if err != nil {
		return User{}, err
	}
	return u, tx.Commit()
}

// RemovePhoto forgets profile id's photo (none: nothing to do) and returns
// the profile; the letter on the coloured circle stands for it again.
func (s *Store) RemovePhoto(ctx context.Context, id int64) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_photos WHERE user_id=?`, id); err != nil {
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET photo_version=0 WHERE id=?`, id); err != nil {
		return User{}, err
	}
	u, err := scan(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM users WHERE id=?`, id))
	if err != nil {
		return User{}, err
	}
	return u, tx.Commit()
}

// Photo is profile id's photo and its version; ErrNotFound without one.
func (s *Store) Photo(ctx context.Context, id int64) (data []byte, version int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT p.data, u.photo_version FROM user_photos p JOIN users u ON u.id = p.user_id WHERE p.user_id=?`, id).Scan(&data, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrNotFound
	}
	return data, version, err
}
