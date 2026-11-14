+++
kind = "repositories"
id = "platform"
title = "The platform"
plan = "platform-v1"
merge_authority = "review_approves"

[[repository]]
name = "api"
remote = "git@github.com:acme/api.git"
path = "../api"
description = "The public API"
review_target = "pull_request"

[[repository]]
name = "web"
path = "../web"
+++

The repositories of the platform.
