# The status page

The page shows the UPS and the nodes right now, and a recorder chart for the last power cut, the last 24 hours or the last 7 days: mains, the battery charge with the wake and shutdown levels, each node, and the Pi. Where the Pi was off, the chart is hatched, because nothing was recording. It also lists the power cuts, with what happened to the nodes in each, and the event log. There is a table version of the chart for screen readers, and a layout for narrow screens.

The page can only read. It answers `GET` and `HEAD`, nothing it is sent is stored or run, and it loads nothing from outside the Pi. Times are shown in the time zone of whoever is looking.
