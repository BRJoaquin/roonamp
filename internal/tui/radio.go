package tui

import (
	"fmt"
	"strings"

	"roonamp/internal/roon"
)

// radioSession is the multi_session_key used for the "start radio" side trip.
// It is deliberately distinct from the browser's "synthetic" session so that
// starting radio from the player never invalidates item_keys the browser has
// cached on its navigation stack.
const radioSession = "radio"

// startSongRadio starts Roon Radio seeded by the currently playing track,
// replacing the current zone's queue. It runs entirely in a dedicated browse
// session (radioSession) so the main and synthetic browse sessions are left
// untouched.
//
// Path: pop_all -> Search(title) -> "Tracks" bucket -> the matching track
// (disambiguated by artist) -> the track's action menu -> "Start Radio". The
// browse "go back" mechanism is unreliable, so each step only ever descends.
func startSongRadio(client *roon.Client, zoneID, title, artist string) error {
	session := radioSession

	if _, err := client.Browse(roon.BrowseRequest{
		Hierarchy:       "browse",
		ZoneOrOutputID:  zoneID,
		PopAll:          true,
		MultiSessionKey: session,
	}); err != nil {
		return fmt.Errorf("pop_all: %w", err)
	}

	searchKey, err := findSearchKey(client, zoneID, session)
	if err != nil {
		return err
	}

	// Search by title; the artist disambiguates within the results.
	if _, err := client.Browse(roon.BrowseRequest{
		Hierarchy:       "browse",
		ZoneOrOutputID:  zoneID,
		ItemKey:         &searchKey,
		Input:           title,
		MultiSessionKey: session,
	}); err != nil {
		return fmt.Errorf("search: %w", err)
	}
	results, err := client.Load(roon.LoadRequest{
		Hierarchy:       "browse",
		Offset:          0,
		Count:           100,
		MultiSessionKey: session,
	})
	if err != nil {
		return fmt.Errorf("load search: %w", err)
	}

	trackKey, err := resolveTrack(client, zoneID, session, results.Items, title, artist)
	if err != nil {
		return err
	}

	// Browsing a track row opens its action menu (Play Now / Queue / Start
	// Radio / ...).
	if _, err := client.Browse(roon.BrowseRequest{
		Hierarchy:       "browse",
		ZoneOrOutputID:  zoneID,
		ItemKey:         &trackKey,
		MultiSessionKey: session,
	}); err != nil {
		return fmt.Errorf("open track menu: %w", err)
	}
	menu, err := client.Load(roon.LoadRequest{
		Hierarchy:       "browse",
		Offset:          0,
		Count:           100,
		MultiSessionKey: session,
	})
	if err != nil {
		return fmt.Errorf("load track menu: %w", err)
	}

	radioKey, err := findRadioActionKey(client, zoneID, session, menu.Items)
	if err != nil {
		return err
	}

	// Triggering an "action" item starts the radio immediately; there is no
	// further list to load.
	if _, err := client.Browse(roon.BrowseRequest{
		Hierarchy:       "browse",
		ZoneOrOutputID:  zoneID,
		ItemKey:         &radioKey,
		MultiSessionKey: session,
	}); err != nil {
		return fmt.Errorf("start radio: %w", err)
	}
	return nil
}

// resolveTrack returns the item_key of the track to seed the radio with. On
// stock Roon the Search page groups hits into Artists / Albums / Tracks
// buckets, so it prefers the "Tracks" bucket; failing that it looks for a
// track-shaped row directly among the top results.
func resolveTrack(client *roon.Client, zoneID, session string, results []roon.BrowseItem, title, artist string) (string, error) {
	if bucket := pickItemByTitlePrefix(results, "Tracks"); bucket != "" {
		if _, err := client.Browse(roon.BrowseRequest{
			Hierarchy:       "browse",
			ZoneOrOutputID:  zoneID,
			ItemKey:         &bucket,
			MultiSessionKey: session,
		}); err != nil {
			return "", fmt.Errorf("browse Tracks: %w", err)
		}
		tracks, err := client.Load(roon.LoadRequest{
			Hierarchy:       "browse",
			Offset:          0,
			Count:           100,
			MultiSessionKey: session,
		})
		if err != nil {
			return "", fmt.Errorf("load Tracks: %w", err)
		}
		if key := pickBestTrack(tracks.Items, title, artist); key != "" {
			return key, nil
		}
	}

	// Fallback: a track row sometimes appears directly among the top results.
	if key := pickBestTrack(results, title, artist); key != "" {
		return key, nil
	}
	return "", fmt.Errorf("no track match for %q", title)
}

// pickBestTrack chooses the track row that best matches the seed song. A row
// whose title matches AND whose subtitle names the artist is preferred, then a
// title-only match, then an artist-only match, then the first row.
func pickBestTrack(items []roon.BrowseItem, title, artist string) string {
	lowerArtist := strings.ToLower(artist)
	var titleOnly, artistOnly, first string
	for i := range items {
		it := &items[i]
		if it.ItemKey == nil {
			continue
		}
		if first == "" {
			first = *it.ItemKey
		}
		titleHit := strings.EqualFold(it.Title, title)
		artistHit := artist != "" && strings.Contains(strings.ToLower(it.Subtitle), lowerArtist)
		switch {
		case titleHit && artistHit:
			return *it.ItemKey
		case titleHit && titleOnly == "":
			titleOnly = *it.ItemKey
		case artistHit && artistOnly == "":
			artistOnly = *it.ItemKey
		}
	}
	if titleOnly != "" {
		return titleOnly
	}
	if artistOnly != "" {
		return artistOnly
	}
	return first
}

// findRadioActionKey locates the "Start Radio" action in an action menu. It is
// usually a direct ("action") item alongside Play Now / Queue / Shuffle, but
// some layouts nest it inside a play action_list. We check for a direct action
// first, then descend into the play action_list once — descending consumes the
// session's position, so only one action_list can be inspected without a fresh
// pop_all.
func findRadioActionKey(client *roon.Client, zoneID, session string, items []roon.BrowseItem) (string, error) {
	if key := pickRadioAction(items); key != "" {
		return key, nil
	}

	listKey := pickPlayActionList(items)
	if listKey == "" {
		return "", fmt.Errorf("no Start Radio action found")
	}

	if _, err := client.Browse(roon.BrowseRequest{
		Hierarchy:       "browse",
		ZoneOrOutputID:  zoneID,
		ItemKey:         &listKey,
		MultiSessionKey: session,
	}); err != nil {
		return "", fmt.Errorf("browse play menu: %w", err)
	}
	sub, err := client.Load(roon.LoadRequest{
		Hierarchy:       "browse",
		Offset:          0,
		Count:           100,
		MultiSessionKey: session,
	})
	if err != nil {
		return "", fmt.Errorf("load play menu: %w", err)
	}
	if key := pickRadioAction(sub.Items); key != "" {
		return key, nil
	}
	return "", fmt.Errorf("no Start Radio action in play menu")
}

// pickRadioAction returns the item_key of the first immediately-triggerable
// ("action") item whose title mentions "radio".
func pickRadioAction(items []roon.BrowseItem) string {
	for i := range items {
		it := &items[i]
		if it.ItemKey == nil || it.Hint != "action" {
			continue
		}
		if strings.Contains(strings.ToLower(it.Title), "radio") {
			return *it.ItemKey
		}
	}
	return ""
}

// pickPlayActionList returns the item_key of a play menu: an action_list whose
// title mentions "play" if present, otherwise the first action_list.
func pickPlayActionList(items []roon.BrowseItem) string {
	var first string
	for i := range items {
		it := &items[i]
		if it.Hint != "action_list" || it.ItemKey == nil {
			continue
		}
		if strings.Contains(strings.ToLower(it.Title), "play") {
			return *it.ItemKey
		}
		if first == "" {
			first = *it.ItemKey
		}
	}
	return first
}
