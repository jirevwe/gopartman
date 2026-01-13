# go_partman

- partition tables by date
- sub-partition tables by tenant id
- tables should have a default partition
- We need to figure out table naming because that's what killed the previous attempt
- We should support both date and tenant id partitioning (how granular though? weekly? monthly? daily? Hmm)
- As long as we can generate partition names, we should be fine
- No AI gen code, lmao, just typing hints as is happening now 