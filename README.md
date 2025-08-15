# Project description of the node dashboard

- Always use the default golang package and folder layout
- cmd folder must contain the main function
- the main function must be small and clean
- Prefer interfaces to structs
- Each function must have a comment
- Use a Makefile to build the application for development and production. When built for production, it should create a linux executable as it will be used in a Dockerfile
- Use github.com/spf13/cobra for command line arguments
- The application must be able to listen on a port and serve a web page which shows metrics like cpu usage, memory usage and disk usage
- The application must read metrics exported by node_exporter
- The web page should use tailwind CSS to create a nice looking dashboard
- The application must provide arguments for configuration: port and node_exporter address