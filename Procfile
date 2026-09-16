# Runway runs the "web" process, and without this file it would run the built
# binary with no arguments - which for a command line tool means printing its
# usage and exiting, which the platform reads as a crash loop.
# See docs/deployment.md and https://www.runway.horse/docs/guides/golang/procfile/
web: chekhov serve
