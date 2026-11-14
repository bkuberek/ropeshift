+++
kind = "contract"
id = "orders-client"
title = "Call the orders service"
reads = ["orders", "schema", "orders"]

[[criteria]]
id = "c1"
text = "The client calls the orders service the schema describes"

[[work_orders]]
id = "orders-client-order"
title = "Add the orders client"
repo = "web"
size = "small"
+++

Build the client against the orders service and its schema.
